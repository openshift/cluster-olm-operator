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
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
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
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-olm-operator/pkg/clients"
	"github.com/openshift/library-go/pkg/operator/loglevel"

	catalogdv1 "github.com/operator-framework/catalogd/api/v1"
)

const (
	// HyperShift environment variables
	HostedKubeconfigSecretEnv = "HOSTED_KUBECONFIG_SECRET"
	HostedNamespaceEnv        = "HOSTED_NAMESPACE"

	// Kubeconfig mount paths
	kubeconfigMountPath = "/var/run/secrets/kubeconfig"
	kubeconfigFilePath  = "/var/run/secrets/kubeconfig/kubeconfig"
)

type Builder struct {
	Assets            string
	Clients           *clients.Clients
	ControllerContext *controllercmd.ControllerContext
	KnownRESTMappings map[schema.GroupVersionKind]*meta.RESTMapping
	FeatureGate       configv1.FeatureGate
}

// IsHyperShiftMode returns true if the operator is running in HyperShift mode.
// HyperShift mode is detected by the presence of the HOSTED_KUBECONFIG_SECRET environment variable.
func (b *Builder) IsHyperShiftMode() bool {
	return os.Getenv(HostedKubeconfigSecretEnv) != ""
}

// GetHostedKubeconfigSecret returns the name of the secret containing the hosted cluster's kubeconfig.
// Returns empty string if not in HyperShift mode.
func (b *Builder) GetHostedKubeconfigSecret() string {
	return os.Getenv(HostedKubeconfigSecretEnv)
}

// GetHostedNamespace returns the hosted control plane namespace.
// Returns empty string if not in HyperShift mode.
func (b *Builder) GetHostedNamespace() string {
	return os.Getenv(HostedNamespaceEnv)
}

func (b *Builder) BuildControllers(subDirectories ...string) (map[string]factory.Controller, map[string]factory.Controller, map[string]factory.Controller, []configv1.ObjectReference, error) {
	var (
		staticResourceControllers = map[string]factory.Controller{}
		deploymentControllers     = map[string]factory.Controller{}
		clusterCatalogControllers = map[string]factory.Controller{}
		relatedObjects            []configv1.ObjectReference
		errs                      []error
	)
	log := klog.FromContext(context.Background()).WithName("BuildControllers")

	titler := cases.Title(language.English)

	for _, subDirectory := range subDirectories {
		var staticResourceFiles []string

		namePrefix := strings.ReplaceAll(titler.String(subDirectory), "-", "")

		sourceDir := filepath.Join(b.Assets, "helm", subDirectory)
		manifestDir := filepath.Join(b.Assets, subDirectory)
		if err := b.renderHelmTemplate(sourceDir, manifestDir); err != nil {
			return nil, nil, nil, nil, fmt.Errorf("failed to render Helm template: %w", err)
		}

		log.Info("Iterating through manifests", "directory", manifestDir)

		if err := filepath.WalkDir(manifestDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() {
				return nil
			}
			if filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml" {
				return nil
			}

			log.Info("Processing YAML", "file", path)

			manifestData, err := os.ReadFile(path)
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

				// Build deployment hooks based on configuration
				deploymentHooks := []deploymentcontroller.DeploymentHookFunc{
					UpdateDeploymentProxyHook(b.Clients.ProxyClient),
				}

				// Add HyperShift kubeconfig injection if in HyperShift mode
				if b.IsHyperShiftMode() {
					kubeconfigSecret := b.GetHostedKubeconfigSecret()
					hostedNamespace := b.GetHostedNamespace()
					log.Info("HyperShift mode detected, injecting kubeconfig configuration",
						"deployment", manifest.GetName(),
						"kubeconfigSecret", kubeconfigSecret,
						"hostedNamespace", hostedNamespace)
					deploymentHooks = append(deploymentHooks,
						InjectHostedClusterKubeconfigHook(kubeconfigSecret, hostedNamespace),
					)
				}

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
					},
					deploymentHooks...,
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
				func(name string) ([]byte, error) { return os.ReadFile(name) },
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

func (b *Builder) UseExperimentalFeatureSet() bool {
	switch b.FeatureGate.Spec.FeatureSet {
	case configv1.CustomNoUpgrade:
		return true
	case configv1.DevPreviewNoUpgrade:
		return true
	case configv1.TechPreviewNoUpgrade:
		return true
	case configv1.Default:
	default:
		klog.FromContext(context.Background()).WithName("builder").Info("Unknown featureSet value, using standard manifests", "featureSet", b.FeatureGate.Spec.FeatureSet)
	}
	return false
}

func (b *Builder) CurrentFeatureGates() (featuregates.FeatureGate, error) {
	enabledFeatures := make([]configv1.FeatureGateName, 10)
	disabledFeatures := make([]configv1.FeatureGateName, 10)
	for _, featureGateValues := range b.FeatureGate.Status.FeatureGates {
		// We don't check featureGateValues.Version... but perhaps we should
		for _, enabled := range featureGateValues.Enabled {
			enabledFeatures = append(enabledFeatures, enabled.Name)
		}
		for _, disabled := range featureGateValues.Disabled {
			disabledFeatures = append(disabledFeatures, disabled.Name)
		}
	}
	// TODO: Replace this with featuregates.NewFeatureGate to use the real thing that panics
	return NewFeatureGate(enabledFeatures, disabledFeatures), nil
}

// TODO: Remove the featureGate stuff to use the real thing
type featureGate struct {
	enabled  []configv1.FeatureGateName
	disabled []configv1.FeatureGateName
}

// TODO: Remove the featureGate stuff to use the real thing
func NewFeatureGate(enabled, disabled []configv1.FeatureGateName) featuregates.FeatureGate {
	return &featureGate{
		enabled:  enabled,
		disabled: disabled,
	}
}

// TODO: Remove the featureGate stuff to use the real thing
func (f *featureGate) Enabled(key configv1.FeatureGateName) bool {
	// Don't panic!
	return slices.Contains(f.enabled, key)
}

// TODO: Remove the featureGate stuff to use the real thing
func (f *featureGate) KnownFeatures() []configv1.FeatureGateName {
	allKnown := make([]configv1.FeatureGateName, 0, len(f.enabled)+len(f.disabled))
	allKnown = append(allKnown, f.enabled...)
	allKnown = append(allKnown, f.disabled...)

	return allKnown
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
		klog.FromContext(context.Background()).WithName("builder").V(1).Info("ProxyHook updating environment", "deployment", deployment.Name)
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

// InjectHostedClusterKubeconfigHook adds the necessary volume, volume mount, and command-line
// arguments to enable an OLMv1 component (catalogd or operator-controller) to watch the
// hosted cluster's API server instead of the management cluster.
//
// This hook is used when cluster-olm-operator runs in HyperShift mode (Approach 1: Control Plane Placement).
// It enables catalogd and operator-controller to connect to the hosted cluster's API server by:
// 1. Mounting the admin-kubeconfig secret as a volume
// 2. Adding volume mounts to all containers
// 3. Adding --kubeconfig and --system-namespace flags to all containers
func InjectHostedClusterKubeconfigHook(kubeconfigSecret, hostedNamespace string) deploymentcontroller.DeploymentHookFunc {
	return func(_ *operatorv1.OperatorSpec, deployment *appsv1.Deployment) error {
		log := klog.FromContext(context.Background()).WithName("builder").WithValues("deployment", deployment.Name)
		log.V(1).Info("Injecting hosted cluster kubeconfig configuration",
			"kubeconfigSecret", kubeconfigSecret,
			"hostedNamespace", hostedNamespace)

		// Add kubeconfig volume from the specified secret
		kubeconfigVolume := corev1.Volume{
			Name: "hosted-kubeconfig",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: kubeconfigSecret,
				},
			},
		}
		deployment.Spec.Template.Spec.Volumes = append(
			deployment.Spec.Template.Spec.Volumes,
			kubeconfigVolume,
		)

		// Add volume mount and command-line flags to all containers
		for i := range deployment.Spec.Template.Spec.Containers {
			container := &deployment.Spec.Template.Spec.Containers[i]

			// Mount kubeconfig
			container.VolumeMounts = append(container.VolumeMounts,
				corev1.VolumeMount{
					Name:      "hosted-kubeconfig",
					MountPath: kubeconfigMountPath,
					ReadOnly:  true,
				},
			)

			// Add command-line flags for kubeconfig and system namespace
			container.Args = append(container.Args,
				"--kubeconfig="+kubeconfigFilePath,
				"--system-namespace="+hostedNamespace,
			)

			log.V(2).Info("Configured container",
				"container", container.Name,
				"kubeconfigPath", kubeconfigFilePath,
				"systemNamespace", hostedNamespace)
		}

		return nil
	}
}
