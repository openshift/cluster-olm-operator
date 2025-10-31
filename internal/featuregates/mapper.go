package featuregates

import (
	"errors"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"

	"github.com/openshift/cluster-olm-operator/pkg/helmvalues"
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
	// WebhookProviderOpenshiftServiceCA: Enables support for the installation of bundles containing webhooks using the openshift-serviceca tls certificate provider
	// WebhookProviderCertManager: This is something that always needs to be disabled downstream
	WebhookProviderOpenshiftServiceCA = "WebhookProviderOpenshiftServiceCA"
	WebhookProviderCertManager        = "WebhookProviderCertManager"
)

type MapperInterface interface {
	UpstreamForDownstream(downstreamGate configv1.FeatureGateName) func(*helmvalues.HelmValues, bool) error
	DownstreamFeatureGates() []configv1.FeatureGateName
}

// Mapper knows the mapping between downstream and upstream feature gates for both OLM components

type gateMapFunc map[configv1.FeatureGateName]func(*helmvalues.HelmValues, bool) error

type Mapper struct {
	featureGates gateMapFunc
}

func NewMapper() *Mapper {
	// Add your downstream to upstream mapping here

	featureGates := gateMapFunc{
		// features.FeatureGateNewOLMMyDownstreamFeature: functon that returns a list of enabled and disabled gates
		// NOTE: Following Kubernetes best practices, we only set feature gates when changing the default behavior.
		// If a feature is disabled by default, we don't explicitly set --feature-gates=FeatureName=false
		features.FeatureGateNewOLMPreflightPermissionChecks: func(v *helmvalues.HelmValues, enabled bool) error {
			if enabled {
				return v.AddListValue(helmvalues.EnableOperatorController, PreflightPermissions)
			}
			// Don't explicitly disable - let it use the upstream default
			return nil
		},
		features.FeatureGateNewOLMOwnSingleNamespace: func(v *helmvalues.HelmValues, enabled bool) error {
			if enabled {
				return v.AddListValue(helmvalues.EnableOperatorController, SingleOwnNamespaceInstallSupport)
			}
			// Don't explicitly disable - let it use the upstream default
			return nil
		},
		features.FeatureGateNewOLMWebhookProviderOpenshiftServiceCA: func(v *helmvalues.HelmValues, enabled bool) error {
			// Always disable CertManager (downstream-specific requirement)
			if err := v.AddListValue(helmvalues.DisableOperatorController, WebhookProviderCertManager); err != nil {
				return err
			}
			if enabled {
				return v.AddListValue(helmvalues.EnableOperatorController, WebhookProviderOpenshiftServiceCA)
			}
			// Don't explicitly disable OpenshiftServiceCA - let it use the upstream default
			return nil
		},
		features.FeatureGateNewOLMCatalogdAPIV1Metas: func(v *helmvalues.HelmValues, enabled bool) error {
			if enabled {
				return v.AddListValue(helmvalues.EnableCatalogd, APIV1MetasHandler)
			}
			// Don't explicitly disable - let it use the upstream default
			return nil
		},
	}

	for _, m := range []gateMapFunc{featureGates} {
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

	return &Mapper{featureGates: featureGates}
}

// DownstreamFeatureGates returns a list of all downstream feature gates
// which have an upstream mapping configured
func (m *Mapper) DownstreamFeatureGates() []configv1.FeatureGateName {
	keys := make([]configv1.FeatureGateName, 0, len(m.featureGates))
	for k := range m.featureGates {
		keys = append(keys, k)
	}
	return keys
}

// UpstreamForDownstream returns upstream feature gates which are configured
// for a given downstream feature gate
func (m *Mapper) UpstreamForDownstream(downstreamGate configv1.FeatureGateName) func(*helmvalues.HelmValues, bool) error {
	return m.featureGates[downstreamGate]
}
