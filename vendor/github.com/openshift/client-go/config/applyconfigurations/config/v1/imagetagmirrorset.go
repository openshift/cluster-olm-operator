// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

import (
	apiconfigv1 "github.com/openshift/api/config/v1"
	internal "github.com/openshift/client-go/config/applyconfigurations/internal"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	managedfields "k8s.io/apimachinery/pkg/util/managedfields"
	v1 "k8s.io/client-go/applyconfigurations/meta/v1"
)

// ImageTagMirrorSetApplyConfiguration represents an declarative configuration of the ImageTagMirrorSet type for use
// with apply.
type ImageTagMirrorSetApplyConfiguration struct {
	v1.TypeMetaApplyConfiguration    `json:",inline"`
	*v1.ObjectMetaApplyConfiguration `json:"metadata,omitempty"`
	Spec                             *ImageTagMirrorSetSpecApplyConfiguration `json:"spec,omitempty"`
	Status                           *apiconfigv1.ImageTagMirrorSetStatus     `json:"status,omitempty"`
}

// ImageTagMirrorSet constructs an declarative configuration of the ImageTagMirrorSet type for use with
// apply.
func ImageTagMirrorSet(name string) *ImageTagMirrorSetApplyConfiguration {
	b := &ImageTagMirrorSetApplyConfiguration{}
	b.WithName(name)
	b.WithKind("ImageTagMirrorSet")
	b.WithAPIVersion("config.openshift.io/v1")
	return b
}

// ExtractImageTagMirrorSet extracts the applied configuration owned by fieldManager from
// imageTagMirrorSet. If no managedFields are found in imageTagMirrorSet for fieldManager, a
// ImageTagMirrorSetApplyConfiguration is returned with only the Name, Namespace (if applicable),
// APIVersion and Kind populated. It is possible that no managed fields were found for because other
// field managers have taken ownership of all the fields previously owned by fieldManager, or because
// the fieldManager never owned fields any fields.
// imageTagMirrorSet must be a unmodified ImageTagMirrorSet API object that was retrieved from the Kubernetes API.
// ExtractImageTagMirrorSet provides a way to perform a extract/modify-in-place/apply workflow.
// Note that an extracted apply configuration will contain fewer fields than what the fieldManager previously
// applied if another fieldManager has updated or force applied any of the previously applied fields.
// Experimental!
func ExtractImageTagMirrorSet(imageTagMirrorSet *apiconfigv1.ImageTagMirrorSet, fieldManager string) (*ImageTagMirrorSetApplyConfiguration, error) {
	return extractImageTagMirrorSet(imageTagMirrorSet, fieldManager, "")
}

// ExtractImageTagMirrorSetStatus is the same as ExtractImageTagMirrorSet except
// that it extracts the status subresource applied configuration.
// Experimental!
func ExtractImageTagMirrorSetStatus(imageTagMirrorSet *apiconfigv1.ImageTagMirrorSet, fieldManager string) (*ImageTagMirrorSetApplyConfiguration, error) {
	return extractImageTagMirrorSet(imageTagMirrorSet, fieldManager, "status")
}

func extractImageTagMirrorSet(imageTagMirrorSet *apiconfigv1.ImageTagMirrorSet, fieldManager string, subresource string) (*ImageTagMirrorSetApplyConfiguration, error) {
	b := &ImageTagMirrorSetApplyConfiguration{}
	err := managedfields.ExtractInto(imageTagMirrorSet, internal.Parser().Type("com.github.openshift.api.config.v1.ImageTagMirrorSet"), fieldManager, b, subresource)
	if err != nil {
		return nil, err
	}
	b.WithName(imageTagMirrorSet.Name)

	b.WithKind("ImageTagMirrorSet")
	b.WithAPIVersion("config.openshift.io/v1")
	return b, nil
}

// WithKind sets the Kind field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Kind field is set to the value of the last call.
func (b *ImageTagMirrorSetApplyConfiguration) WithKind(value string) *ImageTagMirrorSetApplyConfiguration {
	b.Kind = &value
	return b
}

// WithAPIVersion sets the APIVersion field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the APIVersion field is set to the value of the last call.
func (b *ImageTagMirrorSetApplyConfiguration) WithAPIVersion(value string) *ImageTagMirrorSetApplyConfiguration {
	b.APIVersion = &value
	return b
}

// WithName sets the Name field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Name field is set to the value of the last call.
func (b *ImageTagMirrorSetApplyConfiguration) WithName(value string) *ImageTagMirrorSetApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.Name = &value
	return b
}

// WithGenerateName sets the GenerateName field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the GenerateName field is set to the value of the last call.
func (b *ImageTagMirrorSetApplyConfiguration) WithGenerateName(value string) *ImageTagMirrorSetApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.GenerateName = &value
	return b
}

// WithNamespace sets the Namespace field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Namespace field is set to the value of the last call.
func (b *ImageTagMirrorSetApplyConfiguration) WithNamespace(value string) *ImageTagMirrorSetApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.Namespace = &value
	return b
}

// WithUID sets the UID field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the UID field is set to the value of the last call.
func (b *ImageTagMirrorSetApplyConfiguration) WithUID(value types.UID) *ImageTagMirrorSetApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.UID = &value
	return b
}

// WithResourceVersion sets the ResourceVersion field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ResourceVersion field is set to the value of the last call.
func (b *ImageTagMirrorSetApplyConfiguration) WithResourceVersion(value string) *ImageTagMirrorSetApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.ResourceVersion = &value
	return b
}

// WithGeneration sets the Generation field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Generation field is set to the value of the last call.
func (b *ImageTagMirrorSetApplyConfiguration) WithGeneration(value int64) *ImageTagMirrorSetApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.Generation = &value
	return b
}

// WithCreationTimestamp sets the CreationTimestamp field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the CreationTimestamp field is set to the value of the last call.
func (b *ImageTagMirrorSetApplyConfiguration) WithCreationTimestamp(value metav1.Time) *ImageTagMirrorSetApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.CreationTimestamp = &value
	return b
}

// WithDeletionTimestamp sets the DeletionTimestamp field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the DeletionTimestamp field is set to the value of the last call.
func (b *ImageTagMirrorSetApplyConfiguration) WithDeletionTimestamp(value metav1.Time) *ImageTagMirrorSetApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.DeletionTimestamp = &value
	return b
}

// WithDeletionGracePeriodSeconds sets the DeletionGracePeriodSeconds field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the DeletionGracePeriodSeconds field is set to the value of the last call.
func (b *ImageTagMirrorSetApplyConfiguration) WithDeletionGracePeriodSeconds(value int64) *ImageTagMirrorSetApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.DeletionGracePeriodSeconds = &value
	return b
}

// WithLabels puts the entries into the Labels field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, the entries provided by each call will be put on the Labels field,
// overwriting an existing map entries in Labels field with the same key.
func (b *ImageTagMirrorSetApplyConfiguration) WithLabels(entries map[string]string) *ImageTagMirrorSetApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	if b.Labels == nil && len(entries) > 0 {
		b.Labels = make(map[string]string, len(entries))
	}
	for k, v := range entries {
		b.Labels[k] = v
	}
	return b
}

// WithAnnotations puts the entries into the Annotations field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, the entries provided by each call will be put on the Annotations field,
// overwriting an existing map entries in Annotations field with the same key.
func (b *ImageTagMirrorSetApplyConfiguration) WithAnnotations(entries map[string]string) *ImageTagMirrorSetApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	if b.Annotations == nil && len(entries) > 0 {
		b.Annotations = make(map[string]string, len(entries))
	}
	for k, v := range entries {
		b.Annotations[k] = v
	}
	return b
}

// WithOwnerReferences adds the given value to the OwnerReferences field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the OwnerReferences field.
func (b *ImageTagMirrorSetApplyConfiguration) WithOwnerReferences(values ...*v1.OwnerReferenceApplyConfiguration) *ImageTagMirrorSetApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	for i := range values {
		if values[i] == nil {
			panic("nil value passed to WithOwnerReferences")
		}
		b.OwnerReferences = append(b.OwnerReferences, *values[i])
	}
	return b
}

// WithFinalizers adds the given value to the Finalizers field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Finalizers field.
func (b *ImageTagMirrorSetApplyConfiguration) WithFinalizers(values ...string) *ImageTagMirrorSetApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	for i := range values {
		b.Finalizers = append(b.Finalizers, values[i])
	}
	return b
}

func (b *ImageTagMirrorSetApplyConfiguration) ensureObjectMetaApplyConfigurationExists() {
	if b.ObjectMetaApplyConfiguration == nil {
		b.ObjectMetaApplyConfiguration = &v1.ObjectMetaApplyConfiguration{}
	}
}

// WithSpec sets the Spec field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Spec field is set to the value of the last call.
func (b *ImageTagMirrorSetApplyConfiguration) WithSpec(value *ImageTagMirrorSetSpecApplyConfiguration) *ImageTagMirrorSetApplyConfiguration {
	b.Spec = value
	return b
}

// WithStatus sets the Status field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Status field is set to the value of the last call.
func (b *ImageTagMirrorSetApplyConfiguration) WithStatus(value apiconfigv1.ImageTagMirrorSetStatus) *ImageTagMirrorSetApplyConfiguration {
	b.Status = &value
	return b
}
