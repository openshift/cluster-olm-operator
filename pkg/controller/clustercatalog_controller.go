package controller

import (
	"fmt"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/client-go/config/clientset/versioned/scheme"
	"github.com/openshift/cluster-olm-operator/pkg/clients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/management"
	"golang.org/x/net/context"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	catalogdv1alpha1 "github.com/operator-framework/catalogd/api/core/v1alpha1"
)

func NewClusterCatalogController(name string, manifest []byte, key types.NamespacedName, operatorClient *clients.OperatorClient, dynamicClient dynamic.Interface, clusterCatalogClient *clients.ClusterCatalogClient, recorder events.Recorder) factory.Controller {
	c := &clusterCatalogController{
		manifest:         manifest,
		name:             name,
		key:              key,
		applyFunc:        defaultApplyFunc(dynamicClient),
		managedFunc:      defaultManagedFunc(operatorClient),
		shouldUpdateFunc: defaultShouldUpdateFunc(),
		catalogGetFunc:   clusterCatalogClient.Get,
	}

	return factory.New().WithSync(c.sync).WithSyncDegradedOnError(operatorClient).WithInformers(operatorClient.Informer(), clusterCatalogClient.Informer()).ToController(c.name, recorder)
}

func defaultApplyFunc(client dynamic.Interface) applyFunc {
	return func(ctx context.Context, key types.NamespacedName, fieldManager string, force bool, gvr schema.GroupVersionResource, manifest []byte) error {
		var resourceInterface dynamic.ResourceInterface = client.Resource(gvr)
		if key.Namespace != "" {
			resourceInterface = client.Resource(gvr).Namespace(key.Namespace)
		}
		_, err := resourceInterface.Patch(
			ctx,
			key.Name,
			types.ApplyPatchType,
			manifest,
			metav1.PatchOptions{
				Force:        ptr.To(force),
				FieldManager: fieldManager,
			})
		return err
	}
}

func defaultManagedFunc(oc *clients.OperatorClient) managedFunc {
	return func() (bool, error) {
		operatorSpec, _, _, err := oc.GetOperatorState()
		if err != nil {
			return false, err
		}
		return management.IsOperatorManaged(operatorSpec.ManagementState) && (operatorSpec.ManagementState != operatorv1.Removed || management.IsOperatorNotRemovable()), nil
	}
}

func defaultShouldUpdateFunc() shouldUpdateFunc {
	return func(manifest []byte, u *unstructured.Unstructured) (bool, error) {
		required, _, err := scheme.Codecs.UniversalDecoder().Decode(manifest, nil, &unstructured.Unstructured{})
		if err != nil {
			return false, fmt.Errorf("decoding manifest: %w", err)
		}

		// we want the inverse of deep derivative to dictate if we should update.
		// i.e If it is not deep derivative, meaning the requirements are not met, we should update
		return !equality.Semantic.DeepDerivative(required.(*unstructured.Unstructured).UnstructuredContent(), u.UnstructuredContent()), nil
	}
}

// applyFunc is a function that is used and expected to perform a
// server side apply operation. if there is an error during the apply operation,
// it is returned.
type applyFunc func(context.Context, types.NamespacedName, string, bool, schema.GroupVersionResource, []byte) error

// managedFunc is a function that returns whether or not the operator
// is managed. Any errors encountered while evaluating if this operator is
// managed are returned.
type managedFunc func() (bool, error)

// shouldUpdateFunc is a function that takes in raw bytes, expected to be a valid
// Kubernetes resource YAML, and compares it to an *unstructured.Unstructured.
// if there is a difference between the raw bytes and the *unstructured.Unstructured
// that warrants an update to the *unstructured.Unstructured, this function will return
// true with a nil error. Any errors encountered during the evaluation will be returned.
type shouldUpdateFunc func([]byte, *unstructured.Unstructured) (bool, error)

// getCatalogFunc is a function that gets a ClusterCatalog resource using the provided
// key. It is returned as a runtime.Object. Any errors encountered during the process
// of fetching the ClusterCatalog resource will be returned.
type getCatalogFunc func(types.NamespacedName) (runtime.Object, error)

// clusterCatalogController is a generic controller for managing ClusterCatalog resources.
//
// This controller manages resources at the field-level, meaning:
// - The fields specified in the manifest provided to this controller will always be
// used. If they are modified by a user on the cluster, they will be reverted by this controller
// - Any fields not specified in the manifest provided to this controller will not be managed.
// Users of the cluster are free to modify them as they please.
type clusterCatalogController struct {
	name             string
	key              types.NamespacedName
	manifest         []byte
	applyFunc        applyFunc
	managedFunc      managedFunc
	shouldUpdateFunc shouldUpdateFunc
	catalogGetFunc   getCatalogFunc
}

func (c *clusterCatalogController) sync(ctx context.Context, _ factory.SyncContext) error {
	logger := klog.FromContext(ctx).WithName(c.name)
	logger.V(4).Info("sync started")
	defer logger.V(4).Info("sync finished")

	managed, err := c.managedFunc()
	if err != nil {
		return fmt.Errorf("checking if operator is managed: %w", err)
	}
	if !managed {
		return nil
	}

	catalog, err := c.catalogGetFunc(c.key)
	if err != nil {
		return fmt.Errorf("fetching clustercatalog %q: %w", c.key, err)
	}

	shouldUpdate, err := c.shouldUpdateFunc(c.manifest, catalog.(*unstructured.Unstructured))
	if err != nil {
		return fmt.Errorf("determining if clustercatalog %q should be updated: %w", c.key, err)
	}

	if !shouldUpdate {
		return nil
	}

	return c.applyFunc(
		ctx,
		c.key,
		c.name,
		true,
		catalogdv1alpha1.GroupVersion.WithResource("clustercatalogs"),
		c.manifest,
	)
}
