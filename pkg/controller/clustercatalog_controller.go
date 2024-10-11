package controller

import (
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-olm-operator/pkg/clients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/management"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"golang.org/x/net/context"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
)

func NewClusterCatalogController(name string, manifest []byte, operatorClient *clients.OperatorClient, dynamicClient dynamic.Interface, recorder events.Recorder) factory.Controller {
	obj, _, err := scheme.Codecs.UniversalDecoder().Decode(manifest, nil, &unstructured.Unstructured{})
	if err != nil {
		panic("TODO: figure me out")
	}

	c := &clusterCatalogController{
		name:           name,
		obj:            obj.(*unstructured.Unstructured),
		operatorClient: operatorClient,
		dynamicClient:  dynamicClient,
		recorder:       recorder,
		resourceCache:  resourceapply.NewResourceCache(),
		// TODO: use the catalogd library to get this info
		gvr: schema.GroupVersionResource{
			Group:    "olm.operatorframework.io",
			Version:  "v1alpha1",
			Resource: "clustercatalogs",
		},
	}

	infFact := dynamicinformer.NewDynamicSharedInformerFactory(dynamicClient, time.Hour)
	inf := infFact.ForResource(c.gvr)

	return factory.New().WithSync(c.sync).WithInformers(operatorClient.Informer(), inf.Informer()).ToController(c.name, c.recorder)
}

// clusterCatalogController is a generic controller for managing ClusterCatalog resources.
//
// This controller manages resources at the field-level, meaning:
// - The fields specified in the manifest provided to this controller will always be
// used. If they are modified by a user on the cluster, they will be reverted by this controller
// - Any fields not specified in the manifest provided to this controller will not be managed.
// Users of the cluster are free to modify them as they please.
type clusterCatalogController struct {
	name string
	// This needs to be an *unstructured.Unstructured
	// to make use of the resourceapply.ApplyUnstructuredResourceImproved
	// function from library-go. There may be a better way to do this, but
	// for now doing this.
	obj            *unstructured.Unstructured
	operatorClient *clients.OperatorClient
	dynamicClient  dynamic.Interface
	recorder       events.Recorder
	resourceCache  resourceapply.ResourceCache
	gvr            schema.GroupVersionResource
}

func (c *clusterCatalogController) sync(ctx context.Context, _ factory.SyncContext) error {
	logger := klog.FromContext(ctx).WithName(c.name)
	logger.V(4).Info("sync started")
	defer logger.V(4).Info("sync finished")

	operatorSpec, _, _, err := c.operatorClient.GetOperatorState()
	if err != nil {
		return err
	}
	if !management.IsOperatorManaged(operatorSpec.ManagementState) && (operatorSpec.ManagementState != operatorv1.Removed || management.IsOperatorNotRemovable()) {
		return nil
	}

	_, _, err = resourceapply.ApplyUnstructuredResourceImproved(ctx,
		c.dynamicClient,
		c.recorder,
		c.obj,
		c.resourceCache,
		c.gvr,
		nil,
		nil)

	return err
}
