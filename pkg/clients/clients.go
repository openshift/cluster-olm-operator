package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	configinformer "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/client-go/config/informers/externalversions/config"
	configinformerv1 "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	operatorv1apply "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"
	operatorclient "github.com/openshift/client-go/operator/clientset/versioned"
	operatorinformers "github.com/openshift/client-go/operator/informers/externalversions"
	"github.com/openshift/library-go/pkg/apiserver/jsonpatch"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	ocv1 "github.com/operator-framework/operator-controller/api/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	internalfeatures "github.com/openshift/cluster-olm-operator/internal/featuregates"
)

const defaultResyncPeriod = 10 * time.Minute

type Clients struct {
	KubeClient                 kubernetes.Interface
	APIExtensionsClient        apiextensionsclient.Interface
	DynamicClient              dynamic.Interface
	RESTMapper                 meta.RESTMapper
	OperatorClient             *OperatorClient
	OperatorInformers          operatorinformers.SharedInformerFactory
	ClusterExtensionClient     *ClusterExtensionClient
	ClusterCatalogClient       *ClusterCatalogClient
	ProxyClient                *ProxyClient
	ConfigClient               configclient.Interface
	KubeInformerFactory        informers.SharedInformerFactory
	ConfigInformerFactory      configinformer.SharedInformerFactory
	KubeInformersForNamespaces v1helpers.KubeInformersForNamespaces
	FeatureGatesAccessor       featuregates.FeatureGateAccess
	FeatureGateMapper          internalfeatures.MapperInterface
}

func New(cc *controllercmd.ControllerContext) (*Clients, error) {
	kubeClient, err := kubernetes.NewForConfig(cc.ProtoKubeConfig)
	if err != nil {
		return nil, err
	}

	apiExtensionsClient, err := apiextensionsclient.NewForConfig(cc.KubeConfig)
	if err != nil {
		return nil, err
	}

	dynClient, err := dynamic.NewForConfig(cc.KubeConfig)
	if err != nil {
		return nil, err
	}

	httpClient, err := rest.HTTPClientFor(cc.KubeConfig)
	if err != nil {
		return nil, err
	}
	rm, err := apiutil.NewDynamicRESTMapper(cc.KubeConfig, httpClient)
	if err != nil {
		return nil, err
	}

	operatorClientset, err := operatorclient.NewForConfig(cc.KubeConfig)
	if err != nil {
		return nil, err
	}

	operatorInformersFactory := operatorinformers.NewSharedInformerFactory(operatorClientset, defaultResyncPeriod)

	opClient := &OperatorClient{
		clientset: operatorClientset,
		informers: operatorInformersFactory,
		clock:     clock.RealClock{},
	}

	configClient, err := configclient.NewForConfig(cc.KubeConfig)
	if err != nil {
		return nil, err
	}

	configInformerFactory := configinformer.NewSharedInformerFactory(configClient, defaultResyncPeriod)

	return &Clients{
		KubeClient:             kubeClient,
		APIExtensionsClient:    apiExtensionsClient,
		DynamicClient:          dynClient,
		RESTMapper:             rm,
		OperatorClient:         opClient,
		OperatorInformers:      operatorInformersFactory,
		ClusterExtensionClient: NewClusterExtensionClient(dynClient),
		ClusterCatalogClient:   NewClusterCatalogClient(dynClient),
		ProxyClient:            NewProxyClient(configInformerFactory),
		ConfigClient:           configClient,
		KubeInformerFactory:    informers.NewSharedInformerFactory(kubeClient, defaultResyncPeriod),
		ConfigInformerFactory:  configInformerFactory,
		FeatureGatesAccessor:   setupFeatureGatesAccessor(kubeClient, configInformerFactory, cc.OperatorNamespace),
		FeatureGateMapper:      internalfeatures.NewMapper(),
	}, nil
}

func (c *Clients) StartInformers(ctx context.Context) {
	go c.FeatureGatesAccessor.Run(ctx)
	c.KubeInformerFactory.Start(ctx.Done())
	c.ConfigInformerFactory.Start(ctx.Done())
	c.OperatorInformers.Start(ctx.Done())
	c.ClusterExtensionClient.factory.Start(ctx.Done())
	c.ClusterCatalogClient.factory.Start(ctx.Done())
	c.ProxyClient.factory.Start(ctx.Done())
	if c.KubeInformersForNamespaces != nil {
		c.KubeInformersForNamespaces.Start(ctx.Done())
	}
}

func (c *Clients) ClientHolder() *resourceapply.ClientHolder {
	cl := resourceapply.NewClientHolder().
		WithKubernetes(c.KubeClient).
		WithDynamicClient(c.DynamicClient).
		WithAPIExtensionsClient(c.APIExtensionsClient)
	if c.KubeInformersForNamespaces != nil {
		cl = cl.WithKubernetesInformers(c.KubeInformersForNamespaces)
	}
	return cl
}

func setupFeatureGatesAccessor(
	kubeClient *kubernetes.Clientset,
	configInformerFactory configinformer.SharedInformerFactory,
	operatorNamespace string,
) featuregates.FeatureGateAccess {
	eventRecorder := events.NewKubeRecorder(
		kubeClient.CoreV1().Events(operatorNamespace),
		"cluster-olm-operator",
		&corev1.ObjectReference{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Namespace:  operatorNamespace,
			Name:       "cluster-olm-operator",
		},
		clock.RealClock{},
	)

	operatorImageVersion := status.VersionForOperatorFromEnv()
	missingVersion := "0.0.1-snapshot"
	featureGateAccessor := featuregates.NewFeatureGateAccess(
		operatorImageVersion,
		missingVersion,
		configInformerFactory.Config().V1().ClusterVersions(), configInformerFactory.Config().V1().FeatureGates(),
		eventRecorder,
	)
	return featureGateAccessor
}

var _ v1helpers.OperatorClientWithFinalizers = &OperatorClient{}

const (
	globalConfigName      = "cluster"
	fieldManager          = "cluster-olm-operator"
	specFieldManager      = "cluster-olm-operator-spec"
	finalizerFieldManager = "cluster-olm-operator-finalizer"
)

type ClusterExtensionClient struct {
	factory  dynamicinformer.DynamicSharedInformerFactory
	informer informers.GenericInformer
}

func (ce ClusterExtensionClient) Informer() informers.GenericInformer {
	return ce.informer
}

func NewClusterExtensionClient(dynClient dynamic.Interface) *ClusterExtensionClient {
	infFact := dynamicinformer.NewDynamicSharedInformerFactory(dynClient, defaultResyncPeriod)
	clusterExtensionGVR := ocv1.GroupVersion.WithResource("clusterextensions")
	inf := infFact.ForResource(clusterExtensionGVR)

	return &ClusterExtensionClient{
		factory:  infFact,
		informer: inf,
	}
}

type ClusterCatalogClient struct {
	factory  dynamicinformer.DynamicSharedInformerFactory
	informer informers.GenericInformer
}

func (cc *ClusterCatalogClient) Informer() cache.SharedIndexInformer {
	return cc.informer.Informer()
}

func (cc *ClusterCatalogClient) Get(key types.NamespacedName) (runtime.Object, error) {
	return cc.informer.Lister().Get(key.Name)
}

func NewClusterCatalogClient(dynClient dynamic.Interface) *ClusterCatalogClient {
	infFact := dynamicinformer.NewDynamicSharedInformerFactory(dynClient, defaultResyncPeriod)
	clusterCatalogGVR := ocv1.GroupVersion.WithResource("clustercatalogs")
	inf := infFact.ForResource(clusterCatalogGVR)

	return &ClusterCatalogClient{
		factory:  infFact,
		informer: inf,
	}
}

type ProxyClientInterface interface {
	Get(key string) (*configv1.Proxy, error)
}

type ProxyClient struct {
	factory  configinformer.SharedInformerFactory
	informer configinformerv1.ProxyInformer
}

func (pc *ProxyClient) Informer() cache.SharedIndexInformer {
	return pc.informer.Informer()
}

func (pc *ProxyClient) Get(key string) (*configv1.Proxy, error) {
	return pc.informer.Lister().Get(key)
}

func NewProxyClient(infFact configinformer.SharedInformerFactory) *ProxyClient {
	inf := config.New(infFact, "", func(options *metav1.ListOptions) {
		options.FieldSelector = "metadata.name=cluster"
	}).V1().Proxies()

	return &ProxyClient{
		factory:  infFact,
		informer: inf,
	}
}

type OperatorClient struct {
	clientset operatorclient.Interface
	informers operatorinformers.SharedInformerFactory
	clock     clock.PassiveClock
}

func (o OperatorClient) Informer() cache.SharedIndexInformer {
	return o.informers.Operator().V1().OLMs().Informer()
}

func (o OperatorClient) ApplyOperatorSpec(ctx context.Context, fieldManager string, applyConfiguration *operatorv1apply.OperatorSpecApplyConfiguration) error {
	if applyConfiguration == nil {
		return fmt.Errorf("applyConfiguration must have a value")
	}

	// Convert apply configuration to OperatorSpec for patch generation
	operatorSpec, err := convertApplyConfigToOperatorSpec(applyConfiguration)
	if err != nil {
		return fmt.Errorf("error converting apply configuration: %w", err)
	}

	instance, err := o.informers.Operator().V1().OLMs().Lister().Get(globalConfigName)
	if err != nil {
		return fmt.Errorf("unable to get operator configuration: %w", err)
	}

	// Check if changes are needed by comparing current spec
	if needsUpdate, err := operatorSpecNeedsUpdate(instance.Spec.OperatorSpec, *operatorSpec); err != nil {
		return fmt.Errorf("error checking if update needed: %w", err)
	} else if !needsUpdate {
		klog.V(2).Info("ApplyOperatorSpec: no changes detected, skipping patch operation")
		return nil
	}

	klog.V(2).Infof("ApplyOperatorSpec: changes detected, applying patch to %s", globalConfigName)

	// Generate patch using the same method as UpdateOperatorSpec
	patch, err := generateOperatorSpecPatch(instance.ResourceVersion, operatorSpec)
	if err != nil {
		return fmt.Errorf("error generating OperatorSpec patch: %w", err)
	}

	_, err = o.clientset.OperatorV1().OLMs().Patch(ctx, globalConfigName, types.ApplyPatchType, patch, metav1.PatchOptions{FieldManager: finalizerFieldManager, Force: ptr.To(true)})
	if err != nil {
		return fmt.Errorf("unable to patch operator spec using fieldManager %q: %w", fieldManager, err)
	}

	klog.V(1).Infof("ApplyOperatorSpec: successfully applied operator spec patch to %s", globalConfigName)

	return nil
}

func (o OperatorClient) ApplyOperatorStatus(ctx context.Context, fieldManager string, desiredStatus *operatorv1apply.OperatorStatusApplyConfiguration) error {
	if desiredStatus == nil {
		panic("desiredStatus is nil")
	}
	desiredOLMStatus := &operatorv1apply.OLMStatusApplyConfiguration{
		OperatorStatusApplyConfiguration: *desiredStatus,
	}

	for i, curr := range desiredOLMStatus.Conditions {
		// panicking so we can quickly find it and fix the source
		if len(ptr.Deref(curr.Type, "")) == 0 {
			panic(fmt.Sprintf(".status.conditions[%d].type is missing", i))
		}
		if len(ptr.Deref(curr.Status, "")) == 0 {
			panic(fmt.Sprintf(".status.conditions[%q].status is missing", *curr.Type))
		}
	}

	instance, err := o.informers.Operator().V1().OLMs().Lister().Get(globalConfigName)
	switch {
	case apierrors.IsNotFound(err):
		// set last transitionTimes and then apply
		// If our cache improperly 404's (the lister wasn't synchronized), then we will improperly reset all the last transition times.
		// This isn't ideal, but we shouldn't hit this case unless a loop isn't waiting for HasSynced.
		v1helpers.SetApplyConditionsLastTransitionTime(o.clock, &desiredOLMStatus.Conditions, nil)
	case err != nil:
		return fmt.Errorf("unable to get operator configuration: %w", err)
	default:
		previouslyDesiredOLMStatus, err := extractOLMStatus(instance, fieldManager)
		if err != nil {
			return err
		}

		// set last transitionTimes to properly calculate a difference
		// It is possible for last transition time to shift a couple times until the cache updates to have the condition[*].status match,
		// but it will eventually settle.  The failing sequence looks like
		/*
			1. type=foo, status=false, time=t0.Now
			2. type=foo, status=true, time=t1.Now
			3. rapid update happens and the cache still indicates #1
			4. type=foo, status=true, time=t2.Now (this *should* be t1.Now)
		*/
		// Eventually the cache updates to see at #2 and we stop applying new times.
		// This only becomes pathological if the condition is also flapping, but if that happens the time should also update.
		switch {
		case desiredOLMStatus.Conditions != nil && previouslyDesiredOLMStatus != nil:
			v1helpers.SetApplyConditionsLastTransitionTime(o.clock, &desiredStatus.Conditions, previouslyDesiredOLMStatus.Conditions)
		case desiredOLMStatus.Conditions != nil && previouslyDesiredOLMStatus == nil:
			v1helpers.SetApplyConditionsLastTransitionTime(o.clock, &desiredStatus.Conditions, nil)
		}

		// canonicalize so the DeepEqual works consistently
		canonicalizeOLMStatus(previouslyDesiredOLMStatus)
		canonicalizeOLMStatus(desiredOLMStatus)

		previouslyDesiredStatusObj, err := toStatusObj(previouslyDesiredOLMStatus)
		if err != nil {
			return err
		}
		desiredStatusObj, err := toStatusObj(desiredOLMStatus)
		if err != nil {
			return err
		}
		if equality.Semantic.DeepEqual(previouslyDesiredStatusObj, desiredStatusObj) {
			// nothing to apply, so return early
			return nil
		}
	}

	desiredObj := operatorv1apply.OLM(globalConfigName).WithStatus(desiredOLMStatus)
	_, err = o.clientset.OperatorV1().OLMs().ApplyStatus(ctx, desiredObj, metav1.ApplyOptions{
		Force:        true,
		FieldManager: fieldManager,
	})
	if err != nil {
		return fmt.Errorf("unable to ApplyStatus for operator using fieldManager %q: %w", fieldManager, err)
	}

	return nil
}

func extractOLMStatus(instance *operatorv1.OLM, fieldManager string) (*operatorv1apply.OLMStatusApplyConfiguration, error) {
	applyInstance, err := operatorv1apply.ExtractOLMStatus(instance, fieldManager)
	if err != nil {
		return nil, fmt.Errorf("unable to extract operator configuration: %w", err)
	}
	if applyInstance == nil {
		return nil, nil
	}
	return applyInstance.Status, nil
}

func canonicalizeOLMStatus(obj *operatorv1apply.OLMStatusApplyConfiguration) {
	if obj == nil {
		return
	}
	slices.SortStableFunc(obj.Conditions, v1helpers.CompareOperatorConditionByType)
	slices.SortStableFunc(obj.Generations, v1helpers.CompareGenerationStatusByKeys)
}

func toStatusObj(in *operatorv1apply.OLMStatusApplyConfiguration) (*operatorv1.OperatorStatus, error) {
	jsonBytes, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	ret := &operatorv1.OperatorStatus{}
	if err := json.Unmarshal(jsonBytes, ret); err != nil {
		return nil, fmt.Errorf("unable to deserialize: %w", err)
	}
	return ret, nil
}

func (o OperatorClient) PatchOperatorStatus(ctx context.Context, jsonPatch *jsonpatch.PatchSet) error {
	if jsonPatch == nil {
		return fmt.Errorf("jsonPatch must have a value")
	}

	jsonPatchBytes, err := jsonPatch.Marshal()
	if err != nil {
		return fmt.Errorf("error marshaling JSON patch: %w", err)
	}

	klog.V(2).Infof("PatchOperatorStatus: applying %d-byte JSON patch to %s status", len(jsonPatchBytes), globalConfigName)

	_, err = o.clientset.OperatorV1().OLMs().Patch(ctx, globalConfigName, types.JSONPatchType, jsonPatchBytes, metav1.PatchOptions{FieldManager: fieldManager, Force: ptr.To(true)}, "/status")
	if err != nil {
		return fmt.Errorf("unable to PatchOperatorStatus for operator using fieldManager %q: %w", fieldManager, err)
	}

	klog.V(1).Infof("PatchOperatorStatus: successfully patched %s status", globalConfigName)

	return nil
}

func (o OperatorClient) GetObjectMeta() (*metav1.ObjectMeta, error) {
	olm, err := o.clientset.OperatorV1().OLMs().Get(context.TODO(), globalConfigName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return &olm.ObjectMeta, nil
}

func (o OperatorClient) GetOperatorState() (*operatorv1.OperatorSpec, *operatorv1.OperatorStatus, string, error) {
	orig, err := o.informers.Operator().V1().OLMs().Lister().Get(globalConfigName)
	if err != nil {
		return nil, nil, "", err
	}

	olm := orig.DeepCopy()
	return &olm.Spec.OperatorSpec, &olm.Status.OperatorStatus, olm.ResourceVersion, nil
}

func (o OperatorClient) GetOperatorStateWithQuorum(ctx context.Context) (*operatorv1.OperatorSpec, *operatorv1.OperatorStatus, string, error) {
	orig, err := o.clientset.OperatorV1().OLMs().Get(ctx, globalConfigName, metav1.GetOptions{})
	if err != nil {
		return nil, nil, "", err
	}

	olm := orig.DeepCopy()
	return &olm.Spec.OperatorSpec, &olm.Status.OperatorStatus, olm.ResourceVersion, nil
}

func (o OperatorClient) UpdateOperatorSpec(ctx context.Context, oldResourceVersion string, in *operatorv1.OperatorSpec) (*operatorv1.OperatorSpec, string, error) {
	patch, err := generateOperatorSpecPatch(oldResourceVersion, in)
	if err != nil {
		return nil, "", fmt.Errorf("error generating patch: %w", err)
	}

	out, err := o.clientset.OperatorV1().OLMs().Patch(ctx, globalConfigName, types.ApplyPatchType, patch, metav1.PatchOptions{FieldManager: specFieldManager, Force: ptr.To(true)})
	if err != nil {
		return nil, "", err
	}
	return &out.Spec.OperatorSpec, out.ResourceVersion, nil
}

func (o OperatorClient) UpdateOperatorStatus(ctx context.Context, oldResourceVersion string, in *operatorv1.OperatorStatus) (*operatorv1.OperatorStatus, error) {
	patch, err := generateOLMPatch(oldResourceVersion, in, "status")
	if err != nil {
		return nil, fmt.Errorf("error generating patch: %w", err)
	}

	out, err := o.clientset.OperatorV1().OLMs().Patch(ctx, globalConfigName, types.ApplyPatchType, patch, metav1.PatchOptions{FieldManager: fieldManager, Force: ptr.To(true)}, "status")
	if err != nil {
		return nil, err
	}
	return &out.Status.OperatorStatus, nil
}

func (o OperatorClient) EnsureFinalizer(ctx context.Context, finalizer string) error {
	if finalizer == "" {
		return fmt.Errorf("finalizer must not be empty")
	}

	instance, err := o.informers.Operator().V1().OLMs().Lister().Get(globalConfigName)
	if err != nil {
		return fmt.Errorf("unable to get operator configuration: %w", err)
	}

	currentFinalizers := instance.GetFinalizers()

	// Check if finalizer already exists
	finalizersSet := sets.New(currentFinalizers...)
	if finalizersSet.Has(finalizer) {
		klog.V(2).Infof("EnsureFinalizer: finalizer %s already exists", finalizer)
		return nil
	}

	newFinalizers := sets.List(finalizersSet.Insert(finalizer))

	patch, err := generateFinalizerPatch(instance.ResourceVersion, newFinalizers)
	if err != nil {
		return fmt.Errorf("error generating finalizer patch: %w", err)
	}

	klog.V(2).Infof("EnsureFinalizer: adding finalizer %s to %s", finalizer, globalConfigName)

	_, err = o.clientset.OperatorV1().OLMs().Patch(ctx, globalConfigName, types.ApplyPatchType, patch, metav1.PatchOptions{FieldManager: finalizerFieldManager, Force: ptr.To(true)})
	if err != nil {
		return fmt.Errorf("unable to patch operator for finalizer %q: %w", finalizer, err)
	}

	klog.V(1).Infof("EnsureFinalizer: successfully added finalizer %s to %s", finalizer, globalConfigName)

	return nil
}

func (o OperatorClient) RemoveFinalizer(ctx context.Context, finalizer string) error {
	if finalizer == "" {
		return fmt.Errorf("finalizer must not be empty")
	}

	instance, err := o.informers.Operator().V1().OLMs().Lister().Get(globalConfigName)
	if err != nil {
		return fmt.Errorf("unable to get operator configuration: %w", err)
	}

	currentFinalizers := instance.GetFinalizers()

	// Check if finalizer exists
	finalizersSet := sets.New(currentFinalizers...)
	if !finalizersSet.Has(finalizer) {
		klog.V(2).Infof("RemoveFinalizer: finalizer %s not found", finalizer)
		return nil
	}

	newFinalizers := sets.List(finalizersSet.Delete(finalizer))

	patch, err := generateFinalizerPatch(instance.ResourceVersion, newFinalizers)
	if err != nil {
		return fmt.Errorf("error generating finalizer patch: %w", err)
	}

	klog.V(2).Infof("RemoveFinalizer: removing finalizer %s from %s", finalizer, globalConfigName)

	_, err = o.clientset.OperatorV1().OLMs().Patch(ctx, globalConfigName, types.ApplyPatchType, patch, metav1.PatchOptions{FieldManager: finalizerFieldManager, Force: ptr.To(true)})
	if err != nil {
		return fmt.Errorf("unable to patch operator for finalizer %q removal: %w", finalizer, err)
	}

	klog.V(1).Infof("RemoveFinalizer: successfully removed finalizer %s from %s", finalizer, globalConfigName)

	return nil
}

func generateOperatorSpecPatch(_ string, operatorSpec *operatorv1.OperatorSpec) ([]byte, error) {
	var u unstructured.Unstructured
	u.SetAPIVersion(schema.GroupVersion{Group: operatorv1.GroupName, Version: "v1"}.String())
	u.SetKind("OLM")
	// NOTE: Do NOT set resourceVersion in metadata to avoid conflicting with finalizer patches
	// that also modify metadata fields using the same field manager

	// Convert the OperatorSpec to unstructured to properly handle RawExtension fields
	specUnstructured, err := runtime.DefaultUnstructuredConverter.ToUnstructured(operatorSpec)
	if err != nil {
		return nil, fmt.Errorf("error converting OperatorSpec to unstructured: %w", err)
	}

	// Create the spec object in the patch with all OperatorSpec fields
	specObj := map[string]interface{}{
		"managementState":  string(operatorSpec.ManagementState),
		"logLevel":         string(operatorSpec.LogLevel),
		"operatorLogLevel": string(operatorSpec.OperatorLogLevel),
	}

	// Handle RawExtension fields properly
	if unsupportedConfig, found := specUnstructured["unsupportedConfigOverrides"]; found {
		specObj["unsupportedConfigOverrides"] = unsupportedConfig
	} else {
		specObj["unsupportedConfigOverrides"] = nil
	}

	if observedConfig, found := specUnstructured["observedConfig"]; found {
		specObj["observedConfig"] = observedConfig
		if klog.V(3).Enabled() {
			if configBytes, err := json.Marshal(observedConfig); err == nil {
				klog.V(3).Infof("OperatorSpec patch: observedConfig content: %s", string(configBytes))
			}
		}
	} else {
		specObj["observedConfig"] = nil
	}

	// Set the spec field with only OperatorSpec fields
	if err := unstructured.SetNestedField(u.Object, specObj, "spec"); err != nil {
		return nil, fmt.Errorf("error setting spec: %w", err)
	}

	patchBytes, err := json.Marshal(u.Object)
	if err != nil {
		return nil, fmt.Errorf("error marshaling patch: %w", err)
	}

	klog.V(2).Infof("OperatorSpec patch: generated %d-byte patch for operator spec", len(patchBytes))

	return patchBytes, nil
}

func generateOLMPatch(resourceVersion string, in any, fieldPath ...string) ([]byte, error) {
	var u unstructured.Unstructured
	u.SetAPIVersion(schema.GroupVersion{Group: operatorv1.GroupName, Version: "v1"}.String())
	u.SetKind("OLM")
	u.SetResourceVersion(resourceVersion)

	inUnstructured, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&in)
	if err != nil {
		return nil, fmt.Errorf("error converting to unstructured: %w", err)
	}

	fieldPathStr := strings.Join(fieldPath, ".")
	if err := unstructured.SetNestedField(u.Object, inUnstructured, fieldPath...); err != nil {
		return nil, fmt.Errorf("error setting %q: %w", fieldPathStr, err)
	}

	patchBytes, err := json.Marshal(u.Object)
	if err != nil {
		return nil, fmt.Errorf("error marshaling patch: %w", err)
	}

	klog.V(2).Infof("OLM patch: generated %d-byte patch for field path '%s'", len(patchBytes), fieldPathStr)

	return patchBytes, nil
}

// generateFinalizerPatch creates a patch that only modifies the finalizers field
// without affecting other fields that might be managed by the same field manager
func generateFinalizerPatch(resourceVersion string, finalizers []string) ([]byte, error) {
	var u unstructured.Unstructured
	u.SetAPIVersion(schema.GroupVersion{Group: operatorv1.GroupName, Version: "v1"}.String())
	u.SetKind("OLM")
	u.SetResourceVersion(resourceVersion)

	// Set only the finalizers field in metadata
	if err := unstructured.SetNestedStringSlice(u.Object, finalizers, "metadata", "finalizers"); err != nil {
		return nil, fmt.Errorf("error setting finalizers: %w", err)
	}

	patchBytes, err := json.Marshal(u.Object)
	if err != nil {
		return nil, fmt.Errorf("error marshaling finalizer patch: %w", err)
	}

	klog.V(2).Infof("Finalizer patch: generated %d-byte patch with %d finalizers", len(patchBytes), len(finalizers))

	return patchBytes, nil
}

// convertApplyConfigToOperatorSpec converts an OperatorSpecApplyConfiguration to an OperatorSpec
func convertApplyConfigToOperatorSpec(applyConfig *operatorv1apply.OperatorSpecApplyConfiguration) (*operatorv1.OperatorSpec, error) {
	// Marshal apply configuration to JSON
	jsonBytes, err := json.Marshal(applyConfig)
	if err != nil {
		return nil, fmt.Errorf("error marshaling apply configuration: %w", err)
	}

	// Unmarshal to OperatorSpec
	var operatorSpec operatorv1.OperatorSpec
	if err := json.Unmarshal(jsonBytes, &operatorSpec); err != nil {
		return nil, fmt.Errorf("error unmarshaling to OperatorSpec: %w", err)
	}

	return &operatorSpec, nil
}

// operatorSpecNeedsUpdate checks if the current OperatorSpec differs from the desired OperatorSpec
func operatorSpecNeedsUpdate(current, desired operatorv1.OperatorSpec) (bool, error) {
	// Compare management state
	if current.ManagementState != desired.ManagementState {
		klog.V(2).Infof("ManagementState differs: current=%s, desired=%s", current.ManagementState, desired.ManagementState)
		return true, nil
	}

	// Compare log levels
	if current.LogLevel != desired.LogLevel {
		klog.V(2).Infof("LogLevel differs: current=%s, desired=%s", current.LogLevel, desired.LogLevel)
		return true, nil
	}

	if current.OperatorLogLevel != desired.OperatorLogLevel {
		klog.V(2).Infof("OperatorLogLevel differs: current=%s, desired=%s", current.OperatorLogLevel, desired.OperatorLogLevel)
		return true, nil
	}

	// Compare RawExtension fields by converting to unstructured for comparison
	currentUnsupportedConfig, err := rawExtensionToUnstructured(current.UnsupportedConfigOverrides)
	if err != nil {
		return false, fmt.Errorf("error converting current unsupportedConfigOverrides: %w", err)
	}

	desiredUnsupportedConfig, err := rawExtensionToUnstructured(desired.UnsupportedConfigOverrides)
	if err != nil {
		return false, fmt.Errorf("error converting desired unsupportedConfigOverrides: %w", err)
	}

	if !equality.Semantic.DeepEqual(currentUnsupportedConfig, desiredUnsupportedConfig) {
		klog.V(2).Info("UnsupportedConfigOverrides differs")
		return true, nil
	}

	// Compare observedConfig
	currentObservedConfig, err := rawExtensionToUnstructured(current.ObservedConfig)
	if err != nil {
		return false, fmt.Errorf("error converting current observedConfig: %w", err)
	}

	desiredObservedConfig, err := rawExtensionToUnstructured(desired.ObservedConfig)
	if err != nil {
		return false, fmt.Errorf("error converting desired observedConfig: %w", err)
	}

	if !equality.Semantic.DeepEqual(currentObservedConfig, desiredObservedConfig) {
		klog.V(2).Info("ObservedConfig differs")
		return true, nil
	}

	return false, nil
}

// rawExtensionToUnstructured converts a RawExtension to unstructured data for comparison
func rawExtensionToUnstructured(raw runtime.RawExtension) (interface{}, error) {
	if len(raw.Raw) == 0 {
		return nil, nil
	}

	var result interface{}
	if err := json.Unmarshal(raw.Raw, &result); err != nil {
		return nil, fmt.Errorf("error unmarshaling RawExtension: %w", err)
	}

	return result, nil
}
