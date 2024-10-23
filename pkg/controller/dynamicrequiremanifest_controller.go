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
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

type ResourceClient interface {
	Get(types.NamespacedName) (runtime.Object, error)
	Informer() cache.SharedIndexInformer
}

func NewDynamicRequiredManifestController(name string, manifest []byte, key types.NamespacedName, gvr schema.GroupVersionResource, operatorClient *clients.OperatorClient, dynamicClient dynamic.Interface, resourceClient ResourceClient, recorder events.Recorder) factory.Controller {
	c := &dynamicRequiredManifestController{
		manifest:         manifest,
		name:             name,
		key:              key,
		gvr:              gvr,
		applyFunc:        defaultApplyFunc(dynamicClient),
		managedFunc:      defaultManagedFunc(operatorClient),
		shouldUpdateFunc: unstructuredShouldUpdateFunc(),
		objectGetFunc:    resourceClient.Get,
	}

	return factory.New().WithSync(c.sync).WithSyncDegradedOnError(operatorClient).WithInformers(operatorClient.Informer(), resourceClient.Informer()).ToController(c.name, recorder)
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

func unstructuredShouldUpdateFunc() shouldUpdateFunc {
	return func(manifest []byte, existing runtime.Object) (bool, error) {
		if existing == nil {
			return true, nil
		}

		existingUnstructured, ok := existing.(*unstructured.Unstructured)
		if !ok {
			return false, fmt.Errorf("expected existing to be of type *unstructured.Unstructured but was %T", existing)
		}

		// Decode takes in a target type that is not guaranteed to be populated. Since we want an *unstructured.Unstructured type from decoding the
		// manifest bytes, we pass in &unstructured.Unstructured and assume that the returned object is indeed an *unstructured.Unstructured
		// For more information on the Decoder interface, see https://pkg.go.dev/k8s.io/apimachinery@v0.31.1/pkg/runtime#Decoder
		required, _, err := scheme.Codecs.UniversalDecoder().Decode(manifest, nil, &unstructured.Unstructured{})
		if err != nil {
			return false, fmt.Errorf("decoding manifest: %w", err)
		}

		// we want the inverse of deep derivative to dictate if we should update.
		// i.e If it is not deep derivative, meaning the requirements are not met, we should update
		return !equality.Semantic.DeepDerivative(required.(*unstructured.Unstructured).UnstructuredContent(), existingUnstructured), nil
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
type shouldUpdateFunc func([]byte, runtime.Object) (bool, error)

// getObjectFunc is a function that gets a runtime.Object for the managed resource
// using the provided key. Any errors encountered during the process
// of fetching the managed resource will be returned.
type getObjectFunc func(types.NamespacedName) (runtime.Object, error)

// dynamicRequiredManifestController is a generic controller for managing required resources
// via their raw YAML manifests.
//
// This controller manages resources at the field-level, meaning:
// - The fields specified in the manifest provided to this controller will always be
// used. If they are modified by a user on the cluster, they will be reverted by this controller
// - Any fields not specified in the manifest provided to this controller will not be managed.
// Users of the cluster are free to modify them as they please.
type dynamicRequiredManifestController struct {
	name             string
	key              types.NamespacedName
	gvr              schema.GroupVersionResource
	manifest         []byte
	applyFunc        applyFunc
	managedFunc      managedFunc
	shouldUpdateFunc shouldUpdateFunc
	objectGetFunc    getObjectFunc
}

func (c *dynamicRequiredManifestController) sync(ctx context.Context, _ factory.SyncContext) error {
	logger := klog.FromContext(ctx).WithName(c.name)
	logger.V(2).Info("sync started")
	defer logger.V(2).Info("sync finished")

	managed, err := c.managedFunc()
	if err != nil {
		return fmt.Errorf("checking if operator is managed: %w", err)
	}
	if !managed {
		logger.V(2).Info("not managed, skipping sync")
		return nil
	}

	obj, err := c.objectGetFunc(c.key)
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("fetching %s %q: %w", c.gvr, c.key, err)
	}

	// in the event the catalog was not found, the supplied for the existing is nil and
	// shouldUpdateFunc is expected to return true.
	shouldUpdate, err := c.shouldUpdateFunc(c.manifest, obj)
	if err != nil {
		return fmt.Errorf("determining if %s %q should be updated: %w", c.gvr, c.key, err)
	}

	if !shouldUpdate {
		logger.V(4).Info("no updates needed")
		return nil
	}

	logger.V(2).Info(fmt.Sprintf("%s %q does not meet requirements, applying ...", c.gvr, c.key))
	return c.applyFunc(
		ctx,
		c.key,
		c.name,
		true,
		c.gvr,
		c.manifest,
	)
}
