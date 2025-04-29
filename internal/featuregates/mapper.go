package featuregates

import (
	"bytes"
	"errors"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
)

// Add your new upstream feature gate here
const (
	// ref:
	// 1. https://github.com/operator-framework/operator-controller/pull/1643
	// 2. https://github.com/operator-framework/operator-controller/commit/5965d5c9ee56e9077dca39afa59047ece84ed97e#diff-bfcbe63805e38aeb1d57481bd753566c7ddf58702829e1c1ffd7698bd047de67R309
	APIV1MetasHandler    = "APIV1MetasHandler"
	PreflightPermissions = "PreflightPermissions"
	// SingleOwnNamespaceInstallSupport: Enables support for Single- and OwnNamespace install modes.
	SingleOwnNamespaceInstallSupport = "SingleOwnNamespaceInstallSupport"
)

type MapperInterface interface {
	OperatorControllerUpstreamForDownstream(downstreamGate configv1.FeatureGateName) []string
	OperatorControllerDownstreamFeatureGates() []configv1.FeatureGateName
	CatalogdUpstreamForDownstream(downstreamGate configv1.FeatureGateName) []string
	CatalogdDownstreamFeatureGates() []configv1.FeatureGateName
}

// Mapper knows the mapping between downstream and upstream feature gates for both OLM components
type Mapper struct {
	operatorControllerGates map[configv1.FeatureGateName][]string
	catalogdGates           map[configv1.FeatureGateName][]string
}

func NewMapper() *Mapper {
	// Add your downstream to upstream mapping here
	operatorControllerGates := map[configv1.FeatureGateName][]string{
		// features.FeatureGateNewOLMMyDownstreamFeature: {MyUpstreamControllerOperatorFeature}
		features.FeatureGateNewOLMPreflightPermissionChecks: {PreflightPermissions},
		features.FeatureGateNewOLMOwnSingleNamespace:        {SingleOwnNamespaceInstallSupport},
	}
	catalogdGates := map[configv1.FeatureGateName][]string{
		// features.FeatureGateNewOLMMyDownstreamFeature: {MyUpstreamCatalogdFeature}
		features.FeatureGateNewOLMCatalogdAPIV1Metas: {APIV1MetasHandler},
	}

	for _, m := range []map[configv1.FeatureGateName][]string{operatorControllerGates, catalogdGates} {
		for downstreamGate := range m {
			// features.FeatureGateNewOLM is a GA-enabled downstream feature gate.
			// If there is a need to enable upstream alpha/beta features in the downstream GA release
			// get approval via a merged openshift/enhancement describing the need, then carve out
			// an exception in this failsafe code
			if downstreamGate == features.FeatureGateNewOLM {
				panic(errors.New("FeatureGateNewOLM used in mappings"))
			}
			if !strings.HasPrefix(string(downstreamGate), string(features.FeatureGateNewOLM)) {
				panic(errors.New("all downstream feature gates must use NewOLM prefix by convention"))
			}
		}
	}

	return &Mapper{operatorControllerGates: operatorControllerGates, catalogdGates: catalogdGates}
}

// OperatorControllerDownstreamFeatureGates returns a list of all downstream feature gates
// which have an upstream mapping configured for the operator-controller component
func (m *Mapper) OperatorControllerDownstreamFeatureGates() []configv1.FeatureGateName {
	return getKeys(m.operatorControllerGates)
}

// CatalogdDownstreamFeatureGates returns a list of all downstream feature gates
// which have an upstream mapping configured for the catalogd component
func (m *Mapper) CatalogdDownstreamFeatureGates() []configv1.FeatureGateName {
	return getKeys(m.catalogdGates)
}

// OperatorControllerUpstreamForDownstream returns upstream feature gates which are configured
// for a given downstream feature gate for the operator-controller component
func (m *Mapper) OperatorControllerUpstreamForDownstream(downstreamGate configv1.FeatureGateName) []string {
	return m.operatorControllerGates[downstreamGate]
}

// CatalogdUpstreamForDownstream returns upstream feature gates which are configured
// for a given downstream feature gate for the catalogd component
func (m *Mapper) CatalogdUpstreamForDownstream(downstreamGate configv1.FeatureGateName) []string {
	return m.catalogdGates[downstreamGate]
}

// FormatAsEnabledArgs combines list of feature gate names into
// an all-enabled arg format of <feature_gate_name1>=true,<feature_gate_name1>=true etc.
func FormatAsEnabledArgs(enabledFeatureGates []string) string {
	buf := bytes.Buffer{}
	for _, gateName := range enabledFeatureGates {
		buf.WriteString(gateName)
		buf.WriteRune('=')
		buf.WriteString("true")
		buf.WriteRune(',')
	}
	if buf.Len() > 0 {
		// get rid of trailing ','
		buf.Truncate(buf.Len() - 1)
	}

	return buf.String()
}

func getKeys(m map[configv1.FeatureGateName][]string) []configv1.FeatureGateName {
	keys := make([]configv1.FeatureGateName, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
