package clients

import (
	"context"
	"encoding/json"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	configinformer "github.com/openshift/client-go/config/informers/externalversions"
	operatorv1alpha1apply "github.com/openshift/client-go/operator/applyconfigurations/operator/v1alpha1"
	operatorclient "github.com/openshift/client-go/operator/clientset/versioned"
	operatorinformers "github.com/openshift/client-go/operator/informers/externalversions"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

const defaultResyncPeriod = 10 * time.Minute

type Clients struct {
	KubeClient            kubernetes.Interface
	APIExtensionsClient   apiextensionsclient.Interface
	DynamicClient         dynamic.Interface
	RESTMapper            meta.RESTMapper
	OperatorClient        v1helpers.OperatorClientWithFinalizers
	ConfigClient          configclient.Interface
	KubeInformerFactory   informers.SharedInformerFactory
	ConfigInformerFactory configinformer.SharedInformerFactory
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

	opClient, err := NewOperatorClient(cc.KubeConfig)
	if err != nil {
		return nil, err
	}

	configClient, err := configclient.NewForConfig(cc.KubeConfig)
	if err != nil {
		return nil, err
	}

	return &Clients{
		KubeClient:            kubeClient,
		APIExtensionsClient:   apiExtensionsClient,
		DynamicClient:         dynClient,
		RESTMapper:            rm,
		OperatorClient:        opClient,
		ConfigClient:          configClient,
		KubeInformerFactory:   informers.NewSharedInformerFactory(kubeClient, defaultResyncPeriod),
		ConfigInformerFactory: configinformer.NewSharedInformerFactory(configClient, defaultResyncPeriod),
	}, nil
}

func (c *Clients) ClientHolder() *resourceapply.ClientHolder {
	return resourceapply.NewClientHolder().
		WithKubernetes(c.KubeClient).
		WithDynamicClient(c.DynamicClient).
		WithAPIExtensionsClient(c.APIExtensionsClient)
}

func NewOperatorClient(config *rest.Config) (*OperatorClient, error) {
	client, err := operatorclient.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	factory := operatorinformers.NewSharedInformerFactory(client, defaultResyncPeriod)

	return &OperatorClient{
		client:    client,
		informers: factory,
	}, nil
}

var _ v1helpers.OperatorClientWithFinalizers = &OperatorClient{}

const (
	olmSingletonName = "cluster"
	fieldManager     = "cluster-olm-operator"
)

type OperatorClient struct {
	client    operatorclient.Interface
	informers operatorinformers.SharedInformerFactory
}

func (o OperatorClient) Informer() cache.SharedIndexInformer {
	return o.informers.Operator().V1alpha1().OLMs().Informer()
}

func (o OperatorClient) GetObjectMeta() (*metav1.ObjectMeta, error) {
	olm, err := o.client.OperatorV1alpha1().OLMs().Get(context.TODO(), olmSingletonName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return &olm.ObjectMeta, nil
}

func (o OperatorClient) GetOperatorState() (*operatorv1.OperatorSpec, *operatorv1.OperatorStatus, string, error) {
	olm, err := o.informers.Operator().V1alpha1().OLMs().Lister().Get(olmSingletonName)
	if err != nil {
		return nil, nil, "", err
	}

	return &olm.Spec.OperatorSpec, &olm.Status.OperatorStatus, olm.ResourceVersion, nil
}

func (o OperatorClient) UpdateOperatorSpec(ctx context.Context, oldResourceVersion string, in *operatorv1.OperatorSpec) (*operatorv1.OperatorSpec, string, error) {
	olm := &operatorv1alpha1.OLM{
		ObjectMeta: metav1.ObjectMeta{
			Name:            olmSingletonName,
			ResourceVersion: oldResourceVersion,
		},
		Spec: operatorv1alpha1.OLMSpec{OperatorSpec: *in},
	}
	out, err := o.client.OperatorV1alpha1().OLMs().Update(ctx, olm, metav1.UpdateOptions{FieldManager: fieldManager})
	if err != nil {
		return nil, "", err
	}
	return &out.Spec.OperatorSpec, out.ResourceVersion, nil
}

func (o OperatorClient) UpdateOperatorStatus(ctx context.Context, oldResourceVersion string, in *operatorv1.OperatorStatus) (*operatorv1.OperatorStatus, error) {
	olm := &operatorv1alpha1.OLM{
		ObjectMeta: metav1.ObjectMeta{
			Name:            olmSingletonName,
			ResourceVersion: oldResourceVersion,
		},
		Status: operatorv1alpha1.OLMStatus{OperatorStatus: *in},
	}
	out, err := o.client.OperatorV1alpha1().OLMs().UpdateStatus(ctx, olm, metav1.UpdateOptions{FieldManager: fieldManager})
	if err != nil {
		return nil, err
	}
	return &out.Status.OperatorStatus, nil
}

func (o OperatorClient) EnsureFinalizer(ctx context.Context, finalizer string) error {
	olm := operatorv1alpha1apply.OLM(olmSingletonName).WithFinalizers(finalizer)
	patch, err := json.Marshal(olm)
	if err != nil {
		return err
	}
	if _, err := o.client.OperatorV1alpha1().OLMs().Patch(ctx, olmSingletonName, types.ApplyPatchType, patch, metav1.PatchOptions{FieldManager: fieldManager, Force: pointer.Bool(true)}); err != nil {
		return err
	}
	return nil
}

func (o OperatorClient) RemoveFinalizer(ctx context.Context, finalizer string) error {
	olm, err := o.informers.Operator().V1alpha1().OLMs().Lister().Get(olmSingletonName)
	if err != nil {
		return err
	}
	newFinalizers := sets.List(sets.New(olm.GetFinalizers()...).Delete(finalizer))
	olm.SetFinalizers(newFinalizers)

	if _, err := o.client.OperatorV1alpha1().OLMs().Update(ctx, olm, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
		return err
	}
	return nil
}
