package controller

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/openshift/client-go/config/informers/externalversions/config"
	"io/fs"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"os"
	"path/filepath"
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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"

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

	proxyInformer := config.New(b.Clients.ConfigInformerFactory, "", func(options *metav1.ListOptions) {
		options.FieldSelector = "metadata.name=cluster"
	}).V1().Proxies()

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
					[]factory.Informer{proxyInformer.Informer()},
					[]deploymentcontroller.ManifestHookFunc{
						replaceVerbosityHook("${LOG_VERBOSITY}"),
						replaceImageHook("${CATALOGD_IMAGE}", "CATALOGD_IMAGE"),
						replaceImageHook("${OPERATOR_CONTROLLER_IMAGE}", "OPERATOR_CONTROLLER_IMAGE"),
						replaceImageHook("${KUBE_RBAC_PROXY_IMAGE}", "KUBE_RBAC_PROXY_IMAGE"),
					},
					func(spec *operatorv1.OperatorSpec, deployment *appsv1.Deployment) error {
						proxyConfig, err := proxyInformer.Lister().Get("cluster")
						if err != nil {
							return fmt.Errorf("error getting proxies.config.openshift.io/cluster: %w", err)
						}

						setProxyEnvs := func(container *corev1.Container) {
							container.Env = append(container.Env,
								corev1.EnvVar{
									Name:  "HTTP_PROXY",
									Value: proxyConfig.Status.HTTPProxy,
								},
								corev1.EnvVar{
									Name:  "HTTPS_PROXY",
									Value: proxyConfig.Status.HTTPSProxy,
								},
								corev1.EnvVar{
									Name:  "NO_PROXY",
									Value: proxyConfig.Status.NoProxy,
								},
							)
						}

						for i := range deployment.Spec.Template.Spec.InitContainers {
							setProxyEnvs(&deployment.Spec.Template.Spec.InitContainers[i])
						}
						for i := range deployment.Spec.Template.Spec.Containers {
							setProxyEnvs(&deployment.Spec.Template.Spec.Containers[i])
						}
						return nil
					},
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
	return func(spec *operatorv1.OperatorSpec, deployment []byte) ([]byte, error) {
		replacer := strings.NewReplacer(placeholder, os.Getenv(desiredImageEnvVar))
		newDeployment := replacer.Replace(string(deployment))
		return []byte(newDeployment), nil
	}
}
