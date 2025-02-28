// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

import (
	operatorv1 "github.com/openshift/api/operator/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// EtcdSpecApplyConfiguration represents a declarative configuration of the EtcdSpec type for use
// with apply.
type EtcdSpecApplyConfiguration struct {
	StaticPodOperatorSpecApplyConfiguration `json:",inline"`
	HardwareSpeed                           *operatorv1.ControlPlaneHardwareSpeed `json:"controlPlaneHardwareSpeed,omitempty"`
	BackendQuotaGiB                         *int32                                `json:"backendQuotaGiB,omitempty"`
}

// EtcdSpecApplyConfiguration constructs a declarative configuration of the EtcdSpec type for use with
// apply.
func EtcdSpec() *EtcdSpecApplyConfiguration {
	return &EtcdSpecApplyConfiguration{}
}

// WithManagementState sets the ManagementState field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ManagementState field is set to the value of the last call.
func (b *EtcdSpecApplyConfiguration) WithManagementState(value operatorv1.ManagementState) *EtcdSpecApplyConfiguration {
	b.OperatorSpecApplyConfiguration.ManagementState = &value
	return b
}

// WithLogLevel sets the LogLevel field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the LogLevel field is set to the value of the last call.
func (b *EtcdSpecApplyConfiguration) WithLogLevel(value operatorv1.LogLevel) *EtcdSpecApplyConfiguration {
	b.OperatorSpecApplyConfiguration.LogLevel = &value
	return b
}

// WithOperatorLogLevel sets the OperatorLogLevel field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the OperatorLogLevel field is set to the value of the last call.
func (b *EtcdSpecApplyConfiguration) WithOperatorLogLevel(value operatorv1.LogLevel) *EtcdSpecApplyConfiguration {
	b.OperatorSpecApplyConfiguration.OperatorLogLevel = &value
	return b
}

// WithUnsupportedConfigOverrides sets the UnsupportedConfigOverrides field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the UnsupportedConfigOverrides field is set to the value of the last call.
func (b *EtcdSpecApplyConfiguration) WithUnsupportedConfigOverrides(value runtime.RawExtension) *EtcdSpecApplyConfiguration {
	b.OperatorSpecApplyConfiguration.UnsupportedConfigOverrides = &value
	return b
}

// WithObservedConfig sets the ObservedConfig field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ObservedConfig field is set to the value of the last call.
func (b *EtcdSpecApplyConfiguration) WithObservedConfig(value runtime.RawExtension) *EtcdSpecApplyConfiguration {
	b.OperatorSpecApplyConfiguration.ObservedConfig = &value
	return b
}

// WithForceRedeploymentReason sets the ForceRedeploymentReason field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ForceRedeploymentReason field is set to the value of the last call.
func (b *EtcdSpecApplyConfiguration) WithForceRedeploymentReason(value string) *EtcdSpecApplyConfiguration {
	b.StaticPodOperatorSpecApplyConfiguration.ForceRedeploymentReason = &value
	return b
}

// WithFailedRevisionLimit sets the FailedRevisionLimit field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the FailedRevisionLimit field is set to the value of the last call.
func (b *EtcdSpecApplyConfiguration) WithFailedRevisionLimit(value int32) *EtcdSpecApplyConfiguration {
	b.StaticPodOperatorSpecApplyConfiguration.FailedRevisionLimit = &value
	return b
}

// WithSucceededRevisionLimit sets the SucceededRevisionLimit field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the SucceededRevisionLimit field is set to the value of the last call.
func (b *EtcdSpecApplyConfiguration) WithSucceededRevisionLimit(value int32) *EtcdSpecApplyConfiguration {
	b.StaticPodOperatorSpecApplyConfiguration.SucceededRevisionLimit = &value
	return b
}

// WithHardwareSpeed sets the HardwareSpeed field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the HardwareSpeed field is set to the value of the last call.
func (b *EtcdSpecApplyConfiguration) WithHardwareSpeed(value operatorv1.ControlPlaneHardwareSpeed) *EtcdSpecApplyConfiguration {
	b.HardwareSpeed = &value
	return b
}

// WithBackendQuotaGiB sets the BackendQuotaGiB field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the BackendQuotaGiB field is set to the value of the last call.
func (b *EtcdSpecApplyConfiguration) WithBackendQuotaGiB(value int32) *EtcdSpecApplyConfiguration {
	b.BackendQuotaGiB = &value
	return b
}
