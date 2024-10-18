package controller

import (
	"fmt"

	patch "github.com/evanphx/json-patch"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-olm-operator/pkg/clients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/management"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	catalogdv1alpha1 "github.com/operator-framework/catalogd/api/core/v1alpha1"
	"golang.org/x/net/context"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

func NewClusterCatalogController(name string, manifest []byte, operatorClient *clients.OperatorClient, dynamicClient dynamic.Interface, clusterCatalogClient *clients.ClusterCatalogClient, recorder events.Recorder) (factory.Controller, error) {
	obj, _, err := scheme.Codecs.UniversalDecoder().Decode(manifest, nil, &unstructured.Unstructured{})
	if err != nil {
		return nil, fmt.Errorf("decoding manifest data: %w", err)
	}

	uObj := obj.(*unstructured.Unstructured)

	c := &clusterCatalogController{
		name: name,
		key: types.NamespacedName{
			Namespace: uObj.GetNamespace(),
			Name:      uObj.GetName(),
		},
		catalogGetter:  clusterCatalogClient,
		requirementsEnsurer: &metadataSpecEnsurer{
			required: obj.(*unstructured.Unstructured),
		},
		applier: &applier{
			gv:       catalogdv1alpha1.GroupVersion,
			kind:     "ClusterCatalog",
			resource: "clustercatalogs",
			client:   dynamicClient,
			name:     name,
		},
		healthyFunc: clusterCatalogHealthyStatus,
		isManagedFunc: func() (bool, error) {
			operatorSpec, _, _, err := operatorClient.GetOperatorState()
			if err != nil {
				return false, err
			}
			return management.IsOperatorManaged(operatorSpec.ManagementState) && (operatorSpec.ManagementState != operatorv1.Removed || management.IsOperatorNotRemovable()), nil
		},
	}

	return factory.New().WithSync(c.sync).WithSyncDegradedOnError(operatorClient).WithInformers(operatorClient.Informer(), clusterCatalogClient.Informer()).ToController(c.name, recorder), nil
}

// RequirementsEnsurer is an abstraction that is used to represent a
// type that has a method to ensure that required fields are set as expected on a provided
// *unstructured.Unstructured
type RequirementsEnsurer interface {
	// EnsureRequirements ensures that pre-configured requirements are present on the
	// provided *unstructured.Unstructured object, setting them if they are not present
	// or the value does not match the requirement. It returns:
	//   - A boolean that represents whether or not the provided *unstructured.Unstructured was modified (true == modified)
	//   - An error if one occurred during the
	EnsureRequirements(*unstructured.Unstructured) (bool, error)

	// Required returns an *unstructured.Unstructured object matching all the requirements
	Required() *unstructured.Unstructured
}

type metadataSpecEnsurer struct {
	required *unstructured.Unstructured
}

func (mse *metadataSpecEnsurer) Required() *unstructured.Unstructured {
	return mse.required.DeepCopy()
}

func (mse *metadataSpecEnsurer) EnsureRequirements(existing *unstructured.Unstructured) (bool, error) {
	requiredCopy := mse.required.DeepCopy()
	metadataModified, err := ensureRequiredMetadata(requiredCopy, existing)
	if err != nil {
		return false, fmt.Errorf("comparing required and existing metadata for ClusterCatalog %q: %w", mse.required.GetName(), err)
	}

	specModified, err := ensureRequiredSpec(requiredCopy, existing)
	if err != nil {
		return metadataModified, fmt.Errorf("comparing required and existing spec for ClusterCatalog %q: %w", mse.required.GetName(), err)
	}

	return metadataModified || specModified, nil
}

type Applier interface {
	Apply(context.Context, *unstructured.Unstructured, *unstructured.Unstructured) error
}

type applier struct {
	client   dynamic.Interface
	gv       schema.GroupVersion
	kind     string
	resource string
	name     string
}

func (a *applier) Apply(ctx context.Context, original, modified *unstructured.Unstructured) error {
	// do nothing if modified is not provided
	if modified == nil {
		return nil
	}
	// Do a simple create if there is no original
	if original == nil {
		_, err := a.client.Resource(a.gv.WithResource(a.resource)).Create(ctx, modified, metav1.CreateOptions{})
		if err != nil && !errors.IsAlreadyExists(err) {
			return fmt.Errorf("creating resource %s %q: %w", a.gv.WithResource(a.resource), modified.GetName(), err)
		}
		return nil
	}

	// Generate and apply a patch for the diff between original and modified
	diffPatch, err := generatePatch(original, modified)
	if err != nil {
		return fmt.Errorf("generating patch: %w", err)
	}

	_, err = a.client.Resource(a.gv.WithResource(a.resource)).Patch(ctx, original.GetName(), types.MergePatchType, diffPatch, metav1.PatchOptions{
		TypeMeta: metav1.TypeMeta{
			APIVersion: a.gv.String(),
			Kind:       a.kind,
		},
		FieldManager: a.name,
	})
	if err != nil {
		return fmt.Errorf("patching object %s %q with patch %s: %w", a.gv.WithResource(a.resource), original.GetName(), string(diffPatch), err)
	}

	return nil
}

type CatalogGetter interface {
	Get(types.NamespacedName) (runtime.Object, error)
}

// clusterCatalogController is a generic controller for managing ClusterCatalog resources.
//
// This controller manages resources at the field-level, meaning:
// - The fields specified in the manifest provided to this controller will always be
// used. If they are modified by a user on the cluster, they will be reverted by this controller
// - Any fields not specified in the manifest provided to this controller will not be managed.
// Users of the cluster are free to modify them as they please.
type clusterCatalogController struct {
	name                string
	key                 types.NamespacedName
	isManagedFunc       func() (bool, error)
	catalogGetter       CatalogGetter
	requirementsEnsurer RequirementsEnsurer
	healthyFunc         func(*unstructured.Unstructured) error
	applier             Applier
}

func (c *clusterCatalogController) sync(ctx context.Context, _ factory.SyncContext) error {
	logger := klog.FromContext(ctx).WithName(c.name)
	logger.V(4).Info("sync started")
	defer logger.V(4).Info("sync finished")

	managed, err := c.isManagedFunc()
	if err != nil {
		return fmt.Errorf("checking if operator is managed: %w", err)
	}
	if !managed {
		return nil
	}

	// TODO: Instead of using the degraded condition when we want to wait for a certain state
	// it is probably better to set something like a Progressing condition
	existing, err := c.catalogGetter.Get(c.key)
	if errors.IsNotFound(err) {
		applyErr := c.applier.Apply(ctx, nil, c.requirementsEnsurer.Required())
		if applyErr != nil {
			return fmt.Errorf("creating ClusterCatalog %q:%w", c.key, applyErr)
		}
		return fmt.Errorf("created ClusterCatalog %q, waiting for status to be updated", c.key)
	}
	if err != nil {
		return fmt.Errorf("getting ClusterCatalog %q: %w", c.key, err)
	}

	existingCopy := existing.(*unstructured.Unstructured).DeepCopy()
	changed, err := c.requirementsEnsurer.EnsureRequirements(existingCopy)
	if err != nil {
		return fmt.Errorf("ensuring ClusterCatalog %q matches requirements: %w", c.key, err)
	}

	// if we changed something, we should return an error
	// and mark as degraded until we see:
	// - no changes from our requirements
	// - status represents the ClusterCatalog we are managing
	// is in a "healthy" state.
	if changed {
		err = c.applier.Apply(ctx, existing.(*unstructured.Unstructured), existingCopy)
		if err != nil {
			return fmt.Errorf("patching ClusterCatalog %q: %w", c.key, err)
		}
		return fmt.Errorf("requirements not met, ClusterCatalog %q was patched to meet requirements. Marking as degraded until we know requirements are met and ClusterCatalog is healthy", c.key)
	}

	// If nothing has changed, the last thing we need to verify is if the
	// ClusterCatalog is considered "healthy" based on the status conditions.
	// If it is not healthy, an error should be returned to mark this as degraded.
	return c.healthyFunc(existing.(*unstructured.Unstructured))
}

// NOTE: Most, if not all, of this logic was taken from library-go
func ensureRequiredMetadata(required, existing *unstructured.Unstructured) (bool, error) {
	existingMeta, found, err := unstructured.NestedMap(existing.Object, "metadata")
	if err != nil {
		return false, fmt.Errorf("extracting metadata from existing ClusterCatalog %q: %w", required.GetName(), err)
	}
	if !found {
		return false, fmt.Errorf("no metadata found for existing ClusterCatalog %q", required.GetName())
	}

	requiredMeta, found, err := unstructured.NestedMap(required.Object, "metadata")
	if err != nil {
		return false, fmt.Errorf("extracting metadata from required ClusterCatalog %q: %w", required.GetName(), err)
	}
	if !found {
		return false, fmt.Errorf("no metadata found for required ClusterCatalog %q", required.GetName())
	}

	// Cast the metadata to the correct type.
	var existingObjectMetaTyped, requiredObjectMetaTyped metav1.ObjectMeta
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(existingMeta, &existingObjectMetaTyped)
	if err != nil {
		return false, fmt.Errorf("converting existing ClusterCatalog %q metadata to metav1.ObjectMeta: %w", required.GetName(), err)
	}

	err = runtime.DefaultUnstructuredConverter.FromUnstructured(requiredMeta, &requiredObjectMetaTyped)
	if err != nil {
		return false, fmt.Errorf("converting required ClusterCatalog %q metadata to metav1.ObjectMeta: %w", required.GetName(), err)
	}

	// Check if the metadata objects differ.
	didMetadataModify := ptr.To(false)
	resourcemerge.EnsureObjectMeta(didMetadataModify, &existingObjectMetaTyped, requiredObjectMetaTyped)

	mergedMeta, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&existingObjectMetaTyped)
	if err != nil {
		return false, fmt.Errorf("converting merged metadata back to unstructured: %w", err)
	}
	err = unstructured.SetNestedField(existing.Object, mergedMeta, "metadata")
	if err != nil {
		return false, fmt.Errorf("setting existing metadata to merged metadata: %w", err)
	}

	return *didMetadataModify, nil
}

func ensureRequiredSpec(required, existing *unstructured.Unstructured) (bool, error) {
	existingSpec, found, err := unstructured.NestedMap(existing.Object, "spec")
	if err != nil {
		return false, fmt.Errorf("extracting spec from existing ClusterCatalog %q: %w", required.GetName(), err)
	}
	if !found {
		return false, fmt.Errorf("no spec found for existing ClusterCatalog %q", required.GetName())
	}

	requiredSpec, found, err := unstructured.NestedMap(required.Object, "spec")
	if err != nil {
		return false, fmt.Errorf("extracting spec from required ClusterCatalog %q: %w", required.GetName(), err)
	}
	if !found {
		return false, fmt.Errorf("no spec found for required ClusterCatalog %q", required.GetName())
	}

	specModified := ptr.To(false)
	mergeSpecMaps(specModified, &existingSpec, requiredSpec)

	err = unstructured.SetNestedField(existing.Object, existingSpec, "spec")
	if err != nil {
		return false, fmt.Errorf("setting existing spec to merged spec: %w", err)
	}

	return *specModified, nil
}

func generatePatch(original, modified runtime.Object) ([]byte, error) {
	originalJSON, err := runtime.Encode(unstructured.UnstructuredJSONScheme, original)
	if err != nil {
		return []byte{}, fmt.Errorf("unable to decode original to JSON: %w", err)
	}
	modifiedJSON, err := runtime.Encode(unstructured.UnstructuredJSONScheme, modified)
	if err != nil {
		return []byte{}, fmt.Errorf("unable to decode modified to JSON: %w", err)
	}
	return patch.CreateMergePatch(originalJSON, modifiedJSON)
}

// TODO: May need to make this recursive. Need to test if doing something like:
// required:
//
//	spec:
//	  foo:
//	    careabout: bark
//
// existing:
//
//	spec:
//	  foo:
//	    careabout: woof
//	    dontcare: boo
//
// results in:
//
//	spec:
//	  foo:
//	    careabout: bark
//	    dontcare: boo
//
// which is what I would expect. I have a feeling this isn't the case though :P
func mergeSpecMaps(modified *bool, existing *map[string]interface{}, required map[string]interface{}) {
	for key, value := range required {
		existingValue, ok := (*existing)[key]
		// if a required key doesn't exist, add it
		if !ok {
			klog.V(4).Info("mergeSpecMaps ", "required key ", key, " not found in existing")
			*modified = true
			(*existing)[key] = value
			continue
		}
		klog.V(4).Info("mergeSpecMaps ", "required key ", key, " found in existing")
		// if the required value and existing values are not deep derivate,
		// meaning there is a semantic difference between them, set the existing
		// to the required.
		diff := !equality.Semantic.DeepDerivative(value, existingValue)
		klog.V(4).Info("mergeSpecMaps ", "required value ", value, " existing value ", existingValue, " diff ? ", diff)
		if diff {
			*modified = true
			(*existing)[key] = value
		}
	}
}

// clusterCatalogHealthyStatus checks if the provided unstructured.Unstructured (that is expected to be a ClusterCatalog resource)
// has the Serving condition set to True, signalling that the ClusterCatalog
// has been successfully unpacked and is serving it's contents
func clusterCatalogHealthyStatus(obj *unstructured.Unstructured) error {
	catalog := &catalogdv1alpha1.ClusterCatalog{}
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.UnstructuredContent(), catalog)
	if err != nil {
		return fmt.Errorf("converting object to ClusterCatalog type: %w", err)
	}

	cond := meta.FindStatusCondition(catalog.Status.Conditions, catalogdv1alpha1.TypeServing)
	if cond == nil {
		return fmt.Errorf("could not find status condition type %q", catalogdv1alpha1.TypeServing)
	}

	// TODO: should we considered observed generation?
	// if Serving condition observedGeneration = 1 but meta.generation = 6 is that "healthy"
	// because $something is being served?

	// Considered "unhealthy" when Serving != True with Reason == Unavailable.
	if cond.Status != metav1.ConditionTrue && cond.Reason == catalogdv1alpha1.ReasonUnavailable {
		return fmt.Errorf("catalog %q is currently unavailable", catalog.Name)
	}

	// Considered "unhealthy" when Serving != True with Reason == Disabled and the Availability mode doesn't match that it should be disabled
	if cond.Status != metav1.ConditionTrue && cond.Reason == catalogdv1alpha1.ReasonDisabled && catalog.Spec.Availability != catalogdv1alpha1.AvailabilityDisabled {
		return fmt.Errorf("catalog %q is reporting as disabled when it is not marked as disabled", catalog.Name)
	}

	return nil
}
