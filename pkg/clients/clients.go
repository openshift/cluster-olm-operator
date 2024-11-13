package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"strings"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	configinformer "github.com/openshift/client-go/config/informers/externalversions"
	operatorv1apply "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"
	operatorclient "github.com/openshift/client-go/operator/clientset/versioned"
	operatorinformers "github.com/openshift/client-go/operator/informers/externalversions"
	"github.com/openshift/library-go/pkg/apiserver/jsonpatch"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	ocv1alpha1 "github.com/operator-framework/operator-controller/api/v1alpha1"
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

	catalogdv1alpha1 "github.com/operator-framework/catalogd/api/core/v1alpha1"
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
	ConfigClient               configclient.Interface
	KubeInformerFactory        informers.SharedInformerFactory
	ConfigInformerFactory      configinformer.SharedInformerFactory
	KubeInformersForNamespaces v1helpers.KubeInformersForNamespaces
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
	}

	configClient, err := configclient.NewForConfig(cc.KubeConfig)
	if err != nil {
		return nil, err
	}

	return &Clients{
		KubeClient:             kubeClient,
		APIExtensionsClient:    apiExtensionsClient,
		DynamicClient:          dynClient,
		RESTMapper:             rm,
		OperatorClient:         opClient,
		OperatorInformers:      operatorInformersFactory,
		ClusterExtensionClient: NewClusterExtensionClient(dynClient),
		ClusterCatalogClient:   NewClusterCatalogClient(dynClient),
		ConfigClient:           configClient,
		KubeInformerFactory:    informers.NewSharedInformerFactory(kubeClient, defaultResyncPeriod),
		ConfigInformerFactory:  configinformer.NewSharedInformerFactory(configClient, defaultResyncPeriod),
	}, nil
}

func (c *Clients) StartInformers(ctx context.Context) {
	c.KubeInformerFactory.Start(ctx.Done())
	c.ConfigInformerFactory.Start(ctx.Done())
	c.OperatorInformers.Start(ctx.Done())
	c.ClusterExtensionClient.factory.Start(ctx.Done())
	c.ClusterCatalogClient.factory.Start(ctx.Done())
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
	clusterExtensionGVR := ocv1alpha1.GroupVersion.WithResource("clusterextensions")
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
	clusterCatalogGVR := catalogdv1alpha1.GroupVersion.WithResource("clustercatalogs")
	inf := infFact.ForResource(clusterCatalogGVR)

	return &ClusterCatalogClient{
		factory:  infFact,
		informer: inf,
	}
}

type OperatorClient struct {
	clientset operatorclient.Interface
	informers operatorinformers.SharedInformerFactory
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

func (o OperatorClient) ApplyOperatorStatus(ctx context.Context, fieldManager string, applyConfiguration *operatorv1apply.OperatorStatusApplyConfiguration) error {
	if applyConfiguration == nil {
		return fmt.Errorf("applyConfiguration must have a value")
	}

	desiredStatus := &operatorv1apply.OLMStatusApplyConfiguration{
		OperatorStatusApplyConfiguration: *applyConfiguration,
	}
	desired := operatorv1apply.OLM(globalConfigName)
	desired.WithStatus(desiredStatus)

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
		if equality.Semantic.DeepEqual(original.Status, desired.Status) {
			return nil
		}
	}

	v1helpers.SetApplyConditionsLastTransitionTime(o.clock, &applyConfiguration.Conditions, nil)

	_, err = o.clientset.OperatorV1().OLMs().ApplyStatus(ctx, desired, metav1.ApplyOptions{
		Force:        true,
		FieldManager: fieldManager,
	})
	if err != nil {
		return fmt.Errorf("unable to ApplyStatus for operator using fieldManager %q: %w", fieldManager, err)
	}

	return nil
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
