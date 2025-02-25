// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

// ServiceCatalogAPIServerStatusApplyConfiguration represents a declarative configuration of the ServiceCatalogAPIServerStatus type for use
// with apply.
type ServiceCatalogAPIServerStatusApplyConfiguration struct {
	OperatorStatusApplyConfiguration `json:",inline"`
}

// ServiceCatalogAPIServerStatusApplyConfiguration constructs a declarative configuration of the ServiceCatalogAPIServerStatus type for use with
// apply.
func ServiceCatalogAPIServerStatus() *ServiceCatalogAPIServerStatusApplyConfiguration {
	return &ServiceCatalogAPIServerStatusApplyConfiguration{}
}

// WithObservedGeneration sets the ObservedGeneration field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ObservedGeneration field is set to the value of the last call.
func (b *ServiceCatalogAPIServerStatusApplyConfiguration) WithObservedGeneration(value int64) *ServiceCatalogAPIServerStatusApplyConfiguration {
	b.OperatorStatusApplyConfiguration.ObservedGeneration = &value
	return b
}

// WithConditions adds the given value to the Conditions field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Conditions field.
func (b *ServiceCatalogAPIServerStatusApplyConfiguration) WithConditions(values ...*OperatorConditionApplyConfiguration) *ServiceCatalogAPIServerStatusApplyConfiguration {
	for i := range values {
		if values[i] == nil {
			panic("nil value passed to WithConditions")
		}
		b.OperatorStatusApplyConfiguration.Conditions = append(b.OperatorStatusApplyConfiguration.Conditions, *values[i])
	}
	return b
}

// WithVersion sets the Version field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Version field is set to the value of the last call.
func (b *ServiceCatalogAPIServerStatusApplyConfiguration) WithVersion(value string) *ServiceCatalogAPIServerStatusApplyConfiguration {
	b.OperatorStatusApplyConfiguration.Version = &value
	return b
}

// WithReadyReplicas sets the ReadyReplicas field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ReadyReplicas field is set to the value of the last call.
func (b *ServiceCatalogAPIServerStatusApplyConfiguration) WithReadyReplicas(value int32) *ServiceCatalogAPIServerStatusApplyConfiguration {
	b.OperatorStatusApplyConfiguration.ReadyReplicas = &value
	return b
}

// WithLatestAvailableRevision sets the LatestAvailableRevision field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the LatestAvailableRevision field is set to the value of the last call.
func (b *ServiceCatalogAPIServerStatusApplyConfiguration) WithLatestAvailableRevision(value int32) *ServiceCatalogAPIServerStatusApplyConfiguration {
	b.OperatorStatusApplyConfiguration.LatestAvailableRevision = &value
	return b
}

// WithGenerations adds the given value to the Generations field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Generations field.
func (b *ServiceCatalogAPIServerStatusApplyConfiguration) WithGenerations(values ...*GenerationStatusApplyConfiguration) *ServiceCatalogAPIServerStatusApplyConfiguration {
	for i := range values {
		if values[i] == nil {
			panic("nil value passed to WithGenerations")
		}
		b.OperatorStatusApplyConfiguration.Generations = append(b.OperatorStatusApplyConfiguration.Generations, *values[i])
	}
	return b
}
