package controller

import (
	"fmt"

	patch "github.com/evanphx/json-patch"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-olm-operator/pkg/clients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/management"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"golang.org/x/net/context"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
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

func NewClusterCatalogController(name string, manifest []byte, operatorClient *clients.OperatorClient, dynamicClient dynamic.Interface, clusterCatalogClient *clients.ClusterCatalogClient, recorder events.Recorder) factory.Controller {
	obj, _, err := scheme.Codecs.UniversalDecoder().Decode(manifest, nil, &unstructured.Unstructured{})
	if err != nil {
		panic("TODO: figure me out")
	}

	c := &clusterCatalogController{
		name:                 name,
		obj:                  obj.(*unstructured.Unstructured),
		operatorClient:       operatorClient,
		clusterCatalogClient: clusterCatalogClient,
		dynamicClient:        dynamicClient,
		recorder:             recorder,
		resourceCache:        resourceapply.NewResourceCache(),
		// TODO: use the catalogd library to get this info
		gvr: schema.GroupVersionResource{
			Group:    "olm.operatorframework.io",
			Version:  "v1alpha1",
			Resource: "clustercatalogs",
		},
	}

	return factory.New().WithSync(c.sync).WithInformers(operatorClient.Informer(), clusterCatalogClient.Informer()).ToController(c.name, c.recorder)
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
	obj                  *unstructured.Unstructured
	operatorClient       *clients.OperatorClient
	clusterCatalogClient *clients.ClusterCatalogClient
	dynamicClient        dynamic.Interface
	recorder             events.Recorder
	resourceCache        resourceapply.ResourceCache
	gvr                  schema.GroupVersionResource
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

	// TODO: Should this be a live client fetch?
	existing, err := c.clusterCatalogClient.Get(c.obj.GetName())
	if errors.IsNotFound(err) {
		_, err := c.dynamicClient.Resource(c.gvr).Create(ctx, c.obj.DeepCopy(), metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("creating ClusterCatalog %q: %w", c.obj.GetName(), err)
		}
		// Created successfully - move on
		return nil
	}

	if err != nil {
		return fmt.Errorf("getting ClusterCatalog %q: %w", c.obj.GetName(), err)
	}

	existingCopy := existing.(*unstructured.Unstructured).DeepCopy()
	requiredCopy := c.obj.DeepCopy()
	metadataModified, err := compareMetadata(requiredCopy, existingCopy)
	if err != nil {
		return fmt.Errorf("comparing required and existing metadata for ClusterCatalog %q: %w", c.obj.GetName(), err)
	}

	specModified, err := compareSpec(requiredCopy, existingCopy)
	if err != nil {
		return fmt.Errorf("comparing required and existing spec for ClusterCatalog %q: %w", c.obj.GetName(), err)
	}

	if !metadataModified && !specModified {
		klog.V(4).Infof("required and existing ClusterCatalog %q are the same", c.obj.GetName())
		// Nothing to do - they are the same!
		return nil
	}

	klog.V(4).Info("metadata modified? ", metadataModified, " spec modified? ", specModified)

	unstructured.RemoveNestedField(existingCopy.Object, "status")
	unstructured.RemoveNestedField(requiredCopy.Object, "status")

	diffPatch, err := generatePatch(existing, existingCopy)
	if err != nil {
		return fmt.Errorf("generating patch: %w", err)
	}

	klog.V(4).Infof("required and existing ClusterCatalog %q are not the same. patching with patch %s", c.obj.GetName(), string(diffPatch))
	_, err = c.dynamicClient.Resource(c.gvr).Patch(ctx, c.obj.GetName(), types.ApplyPatchType, diffPatch, metav1.PatchOptions{
		TypeMeta: metav1.TypeMeta{
			APIVersion: c.gvr.GroupVersion().String(),
			Kind:       "ClusterCatalog",
		},
		FieldManager: "cluster-olm-operator",
		Force:        ptr.To(true),
	})
    if err != nil {
        return fmt.Errorf("patching existing ClusterCatalog %q: %w", c.obj.GetName(), err)
    }
	return nil
}

// TODO: Do a custom patch (server-side apply) with the unstructured.Unstructured and a dynamic client.
// Also add degraded condition communication for:
// - Not serving (?) - check degraded docs (maybe Progressing == True with reason Retrying)
// - Noticed a change but failed to apply - error returned from sync - would result in degraded

func compareMetadata(required, existing *unstructured.Unstructured) (bool, error) {
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
		return *didMetadataModify, fmt.Errorf("converting merged metadata back to unstructured: %w", err)
	}
	err = unstructured.SetNestedField(existing.Object, mergedMeta, "metadata")
	if err != nil {
		return *didMetadataModify, fmt.Errorf("setting existing metadata to merged metadata: %w", err)
	}

	return *didMetadataModify, nil
}

func compareSpec(required, existing *unstructured.Unstructured) (bool, error) {
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
		return *specModified, fmt.Errorf("setting existing spec to merged spec: %w", err)
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
