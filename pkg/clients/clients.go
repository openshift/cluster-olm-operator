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
	internalfeatures "github.com/openshift/cluster-olm-operator/internal/featuregates"
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

	catalogdv1 "github.com/operator-framework/catalogd/api/v1"
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
	)

	operatorImageVersion := status.VersionForOperatorFromEnv()
	missingVersion := "0.0.1-snapshot"
	featureGateAccessor := featuregates.NewFeatureGateAccess(
		operatorImageVersion,
		missingVersion,
		configInformerFactory.Config().V1().ClusterVersions(), configInformerFactory.Config().V1().FeatureGates(),
		eventRecorder,
	)
	// modify the default behavior of calling exit(0) to noop whenever a FeatureGates set changes in cluster
	// reconsider this change if there ever comes a feature flag that affects the cluster-olm-operator directly
	// see: https://github.com/openshift/cluster-olm-operator/pull/102#discussion_r1926861888
	featureGateAccessor.SetChangeHandler(func(_ featuregates.FeatureChange) {})

	return featureGateAccessor
}

var _ v1helpers.OperatorClientWithFinalizers = &OperatorClient{}

const (
	globalConfigName = "cluster"
	fieldManager     = "cluster-olm-operator"
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
	clusterCatalogGVR := catalogdv1.GroupVersion.WithResource("clustercatalogs")
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

	desiredSpec := &operatorv1apply.OLMSpecApplyConfiguration{
		OperatorSpecApplyConfiguration: *applyConfiguration,
	}
	desired := operatorv1apply.OLM(globalConfigName)
	desired.WithSpec(desiredSpec)

	instance, err := o.informers.Operator().V1().OLMs().Lister().Get(globalConfigName)
	switch {
	case apierrors.IsNotFound(err):
		// do nothing and proceed with the apply
	case err != nil:
		return fmt.Errorf("unable to get operator configuration: %w", err)
	default:
		original, err := operatorv1apply.ExtractOLM(instance, fieldManager)
		if err != nil {
			return fmt.Errorf("unable to extract operator configuration: %w", err)
		}
		if equality.Semantic.DeepEqual(original, desired) {
			return nil
		}
	}

	_, err = o.clientset.OperatorV1().OLMs().Apply(ctx, desired, metav1.ApplyOptions{
		Force:        true,
		FieldManager: fieldManager,
	})
	if err != nil {
		return fmt.Errorf("unable to Apply for operator using fieldManager %q: %w", fieldManager, err)
	}

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
	jsonPatchBytes, err := jsonPatch.Marshal()
	if err != nil {
		return err
	}
	_, err = o.clientset.OperatorV1().OLMs().Patch(ctx, globalConfigName, types.JSONPatchType, jsonPatchBytes, metav1.PatchOptions{}, "/status")
	if err != nil {
		return fmt.Errorf("unable to PatchOperatorStatus for operator using fieldManager %q: %w", fieldManager, err)
	}
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
	patch, err := generateOLMPatch(oldResourceVersion, in, "spec")
	if err != nil {
		return nil, "", fmt.Errorf("error generating patch: %w", err)
	}

	out, err := o.clientset.OperatorV1().OLMs().Patch(ctx, globalConfigName, types.ApplyPatchType, patch, metav1.PatchOptions{FieldManager: fieldManager, Force: ptr.To(true)})
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
	instance, err := o.informers.Operator().V1().OLMs().Lister().Get(globalConfigName)
	if err != nil {
		return err
	}
	newFinalizers := sets.List(sets.New(instance.GetFinalizers()...).Insert(finalizer))

	olm := operatorv1apply.OLM(globalConfigName).WithFinalizers(newFinalizers...)
	patch, err := json.Marshal(olm)
	if err != nil {
		return err
	}

	if _, err := o.clientset.OperatorV1().OLMs().Patch(ctx, globalConfigName, types.ApplyPatchType, patch, metav1.PatchOptions{FieldManager: fieldManager, Force: ptr.To(true)}); err != nil {
		return err
	}
	return nil
}

func (o OperatorClient) RemoveFinalizer(ctx context.Context, finalizer string) error {
	instance, err := o.informers.Operator().V1().OLMs().Lister().Get(globalConfigName)
	if err != nil {
		return err
	}
	newFinalizers := sets.List(sets.New(instance.GetFinalizers()...).Delete(finalizer))

	olm := operatorv1apply.OLM(globalConfigName).WithFinalizers(newFinalizers...)
	patch, err := json.Marshal(olm)
	if err != nil {
		return err
	}

	if _, err := o.clientset.OperatorV1().OLMs().Patch(ctx, globalConfigName, types.ApplyPatchType, patch, metav1.PatchOptions{FieldManager: fieldManager, Force: ptr.To(true)}); err != nil {
		return err
	}
	return nil
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

	if err := unstructured.SetNestedField(u.Object, inUnstructured, fieldPath...); err != nil {
		return nil, fmt.Errorf("error setting %q: %w", strings.Join(fieldPath, "."), err)
	}

	return json.Marshal(u.Object)
}
