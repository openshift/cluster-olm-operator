package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/deploymentcontroller"
	"github.com/openshift/library-go/pkg/operator/staticresourcecontroller"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-olm-operator/pkg/clients"
	"github.com/openshift/library-go/pkg/operator/loglevel"

	catalogdv1 "github.com/operator-framework/catalogd/api/v1"
)

type Builder struct {
	Assets            fs.FS
	Clients           *clients.Clients
	ControllerContext *controllercmd.ControllerContext
	KnownRESTMappings map[schema.GroupVersionKind]*meta.RESTMapping
}

func (b *Builder) BuildControllers(subDirectories ...string) (map[string]factory.Controller, map[string]factory.Controller, map[string]factory.Controller, []configv1.ObjectReference, error) {
	var (
		staticResourceControllers = map[string]factory.Controller{}
		deploymentControllers     = map[string]factory.Controller{}
		clusterCatalogControllers = map[string]factory.Controller{}
		relatedObjects            []configv1.ObjectReference
		errs                      []error
	)

	titler := cases.Title(language.English)
	for _, subDirectory := range subDirectories {
		var staticResourceFiles []string
		namePrefix := strings.ReplaceAll(titler.String(subDirectory), "-", "")
		if err := fs.WalkDir(b.Assets, subDirectory, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() {
				return nil
			}
			if filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml" {
				return nil
			}

			manifestData, err := fs.ReadFile(b.Assets, path)
			if err != nil {
				errs = append(errs, fmt.Errorf("error reading assets file %q: %w", path, err))
				return nil
			}

			var manifest unstructured.Unstructured
			if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifestData), 4096).Decode(&manifest); err != nil {
				errs = append(errs, fmt.Errorf("error parsing manifest for file %q: %w", path, err))
				return nil
			}

			manifestGVK := manifest.GroupVersionKind()
			// check our known mappings first. If there isn't one, fallback to discovery
			restMapping, ok := b.KnownRESTMappings[manifestGVK]
			if !ok {
				restMapping, err = b.Clients.RESTMapper.RESTMapping(manifestGVK.GroupKind(), manifestGVK.Version)
				if err != nil {
					errs = append(errs, fmt.Errorf("error looking up RESTMapping for file %q, gvk %v: %w", path, manifestGVK, err))
					return nil
				}
			}
			relatedObjects = append(relatedObjects, configv1.ObjectReference{
				Group:     restMapping.GroupVersionKind.Group,
				Resource:  restMapping.Resource.Resource,
				Namespace: manifest.GetNamespace(),
				Name:      manifest.GetName(),
			})

			if manifestGVK.Kind == "Deployment" && manifestGVK.Group == "apps" {
				controllerName := controllerNameForObject(namePrefix, &manifest)
				deploymentControllers[controllerName] = deploymentcontroller.NewDeploymentController(
					controllerName,
					manifestData,
					b.ControllerContext.EventRecorder.ForComponent(controllerName),
					b.Clients.OperatorClient,
					b.Clients.KubeClient,
					b.Clients.KubeInformerFactory.Apps().V1().Deployments(),
					[]factory.Informer{
						b.Clients.ProxyClient.Informer(),
					},
					[]deploymentcontroller.ManifestHookFunc{
						replaceVerbosityHook("${LOG_VERBOSITY}"),
						replaceImageHook("${CATALOGD_IMAGE}", "CATALOGD_IMAGE"),
						replaceImageHook("${OPERATOR_CONTROLLER_IMAGE}", "OPERATOR_CONTROLLER_IMAGE"),
						replaceImageHook("${KUBE_RBAC_PROXY_IMAGE}", "KUBE_RBAC_PROXY_IMAGE"),
					},
					UpdateDeploymentProxyHook(b.Clients.ProxyClient),
					UpdateDeploymentFeatureGatesHook(b.Clients.FeatureGatesAccessor, b.Clients.FeatureGateMapper),
				)
				return nil
			}

			if manifestGVK.Kind == "ClusterCatalog" && manifestGVK.Group == catalogdv1.GroupVersion.Group {
				controllerName := controllerNameForObject(namePrefix, &manifest)
				clusterCatalogControllers[controllerName] = NewDynamicRequiredManifestController(
					controllerName,
					manifestData,
					types.NamespacedName{
						Namespace: manifest.GetNamespace(),
						Name:      manifest.GetName(),
					},
					catalogdv1.GroupVersion.WithResource("clustercatalogs"),
					b.Clients.OperatorClient,
					b.Clients.DynamicClient,
					b.Clients.ClusterCatalogClient,
					b.ControllerContext.EventRecorder.ForComponent(controllerName),
				)
				return nil
			}

			staticResourceFiles = append(staticResourceFiles, path)
			return nil
		}); err != nil {
			return nil, nil, nil, nil, err
		}

		if len(staticResourceFiles) > 0 {
			controllerName := fmt.Sprintf("%sStaticResources", namePrefix)
			staticResourceControllers[controllerName] = staticresourcecontroller.NewStaticResourceController(
				controllerName,
				func(name string) ([]byte, error) { return fs.ReadFile(b.Assets, name) },
				staticResourceFiles,
				b.Clients.ClientHolder(),
				b.Clients.OperatorClient,
				b.ControllerContext.EventRecorder.ForComponent(controllerName),
			)
		}
	}
	if len(errs) > 0 {
		return nil, nil, nil, nil, fmt.Errorf("error building controllers: %w", errors.Join(errs...))
	}
	return staticResourceControllers, deploymentControllers, clusterCatalogControllers, relatedObjects, nil
}

type object interface {
	metav1.Object
	runtime.Object
}

func controllerNameForObject(prefix string, obj object) string {
	titler := cases.Title(language.English)
	return fmt.Sprintf("%s%s%s",
		strings.ReplaceAll(titler.String(prefix), "-", ""),
		obj.GetObjectKind().GroupVersionKind().Kind,
		strings.ReplaceAll(titler.String(obj.GetName()), "-", ""),
	)
}

func replaceVerbosityHook(placeholder string) deploymentcontroller.ManifestHookFunc {
	return func(spec *operatorv1.OperatorSpec, deployment []byte) ([]byte, error) {
		desiredVerbosity := loglevel.LogLevelToVerbosity(spec.LogLevel)
		replacer := strings.NewReplacer(placeholder, strconv.Itoa(desiredVerbosity))
		newDeployment := replacer.Replace(string(deployment))
		return []byte(newDeployment), nil
	}
}

func replaceImageHook(placeholder string, desiredImageEnvVar string) deploymentcontroller.ManifestHookFunc {
	return func(_ *operatorv1.OperatorSpec, deployment []byte) ([]byte, error) {
		replacer := strings.NewReplacer(placeholder, os.Getenv(desiredImageEnvVar))
		newDeployment := replacer.Replace(string(deployment))
		return []byte(newDeployment), nil
	}
}

func updateEnv(con *corev1.Container, env corev1.EnvVar) error {
	for _, e := range con.Env {
		if e.Name == env.Name {
			return fmt.Errorf("unexpected environment variable %q=%q in container %q while building manifests", e.Name, e.Value, con.Name)
		}
	}
	if env.Value == "" {
		return nil
	}
	klog.FromContext(context.Background()).WithName("builder").V(4).Info("Updated environment", "container", con.Name, "key", env.Name, "value", env.Value)
	con.Env = append(con.Env, env)
	return nil
}

func setContainerEnv(con *corev1.Container, envs []corev1.EnvVar) error {
	for _, env := range envs {
		if err := updateEnv(con, env); err != nil {
			return err
		}
	}
	return nil
}

func UpdateDeploymentProxyHook(pc clients.ProxyClientInterface) deploymentcontroller.DeploymentHookFunc {
	return func(_ *operatorv1.OperatorSpec, deployment *appsv1.Deployment) error {
		klog.FromContext(context.Background()).WithName("builder").V(0).Info("ProxyHook updating environment", "deployment", deployment.Name)
		proxyConfig, err := pc.Get("cluster")
		if err != nil {
			return fmt.Errorf("error getting proxies.config.openshift.io/cluster: %w", err)
		}

		vars := []corev1.EnvVar{
			{Name: HTTPSProxy, Value: proxyConfig.Status.HTTPSProxy},
			{Name: HTTPProxy, Value: proxyConfig.Status.HTTPProxy},
			{Name: NoProxy, Value: proxyConfig.Status.NoProxy},
		}

		var errs []error
		for i := range deployment.Spec.Template.Spec.InitContainers {
			err = setContainerEnv(&deployment.Spec.Template.Spec.InitContainers[i], vars)
			if err != nil {
				errs = append(errs, err)
			}
		}
		for i := range deployment.Spec.Template.Spec.Containers {
			err = setContainerEnv(&deployment.Spec.Template.Spec.Containers[i], vars)
			if err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) > 0 {
			return errors.Join(errs...)
		}

		return nil
	}
}

// The return behavior:
// 1. If the input arg value matches the container argument = success
// 2. If the input arg value does not match the container argument = error
func setContainerFeatureGateArg(con *corev1.Container, value string) error {
	// Need to remove any existing `--feature-gates` arguments first
	// This could happen because the experimental manifest may have feature-gates already enabled
	const arg = "--feature-gates="
	foundValues := sets.New[string]()

	con.Args = slices.DeleteFunc(con.Args, func(s string) bool {
		values, found := strings.CutPrefix(s, arg)
		if found {
			foundValues.Insert(strings.Split(values, ",")...)
		}
		return found
	})

	haveValues := strings.Join(slices.Sorted(slices.Values(foundValues.UnsortedList())), ",")
	wantValues := strings.Join(slices.Sorted(slices.Values(strings.Split(value, ","))), ",")
	if haveValues != wantValues {
		return fmt.Errorf("argument %q has conflicting values: existing=%q, new=%q", arg, haveValues, wantValues)
	}

	if value != "" {
		con.Args = append(con.Args, arg+value)
	}
	return nil
}
