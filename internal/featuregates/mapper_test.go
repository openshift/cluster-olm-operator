package featuregates

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"

	"github.com/openshift/cluster-olm-operator/pkg/helmvalues"
)

func TestNewMapper(t *testing.T) {
	mapper := NewMapper()
	if mapper == nil {
		t.Fatal("NewMapper returned nil")
	}
	if mapper.featureGates == nil {
		t.Fatal("featureGates map is nil")
	}
	if len(mapper.featureGates) == 0 {
		t.Fatal("featureGates map is empty")
	}
}

func TestMapper_DownstreamFeatureGates(t *testing.T) {
	mapper := NewMapper()
	gates := mapper.DownstreamFeatureGates()

	expectedGates := []configv1.FeatureGateName{
		features.FeatureGateNewOLMPreflightPermissionChecks,
		features.FeatureGateNewOLMOwnSingleNamespace,
		features.FeatureGateNewOLMWebhookProviderOpenshiftServiceCA,
		features.FeatureGateNewOLMCatalogdAPIV1Metas,
	}

	if len(gates) != len(expectedGates) {
		t.Fatalf("Expected %d gates, got %d", len(expectedGates), len(gates))
	}

	for _, expectedGate := range expectedGates {
		if !slices.Contains(gates, expectedGate) {
			t.Errorf("Expected gate %s not found in returned gates", expectedGate)
		}
	}
}

func TestMapper_UpstreamForDownstream(t *testing.T) {
	mapper := NewMapper()

	tests := []struct {
		name           string
		downstreamGate configv1.FeatureGateName
		enabled        bool
		expectFunc     bool
	}{
		{
			name:           "valid downstream gate - preflight permissions",
			downstreamGate: features.FeatureGateNewOLMPreflightPermissionChecks,
			enabled:        true,
			expectFunc:     true,
		},
		{
			name:           "valid downstream gate - own single namespace",
			downstreamGate: features.FeatureGateNewOLMOwnSingleNamespace,
			enabled:        false,
			expectFunc:     true,
		},
		{
			name:           "valid downstream gate - webhook provider",
			downstreamGate: features.FeatureGateNewOLMWebhookProviderOpenshiftServiceCA,
			enabled:        true,
			expectFunc:     true,
		},
		{
			name:           "valid downstream gate - catalogd api v1 metas",
			downstreamGate: features.FeatureGateNewOLMCatalogdAPIV1Metas,
			enabled:        false,
			expectFunc:     true,
		},
		{
			name:           "invalid downstream gate",
			downstreamGate: "InvalidGate",
			enabled:        true,
			expectFunc:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := mapper.UpstreamForDownstream(tt.downstreamGate)
			if tt.expectFunc && fn == nil {
				t.Errorf("Expected function for gate %s, got nil", tt.downstreamGate)
			}
			if !tt.expectFunc && fn != nil {
				t.Errorf("Expected nil function for gate %s, got non-nil", tt.downstreamGate)
			}
		})
	}
}

func TestMapper_FeatureGateMappings(t *testing.T) {
	tests := []struct {
		name           string
		downstreamGate configv1.FeatureGateName
		enabled        bool
		expectedValues map[string]interface{}
	}{
		{
			name:           "PreflightPermissionChecks enabled",
			downstreamGate: features.FeatureGateNewOLMPreflightPermissionChecks,
			enabled:        true,
			expectedValues: map[string]interface{}{
				"options": map[string]interface{}{
					"operatorController": map[string]interface{}{
						"features": map[string]interface{}{
							"enabled": []interface{}{PreflightPermissions},
						},
					},
				},
			},
		},
		{
			name:           "PreflightPermissionChecks disabled",
			downstreamGate: features.FeatureGateNewOLMPreflightPermissionChecks,
			enabled:        false,
			expectedValues: map[string]interface{}{}, // No explicit disable - use upstream default
		},
		{
			name:           "OwnSingleNamespace enabled",
			downstreamGate: features.FeatureGateNewOLMOwnSingleNamespace,
			enabled:        true,
			expectedValues: map[string]interface{}{
				"options": map[string]interface{}{
					"operatorController": map[string]interface{}{
						"features": map[string]interface{}{
							"enabled": []interface{}{SingleOwnNamespaceInstallSupport},
						},
					},
				},
			},
		},
		{
			name:           "OwnSingleNamespace disabled",
			downstreamGate: features.FeatureGateNewOLMOwnSingleNamespace,
			enabled:        false,
			expectedValues: map[string]interface{}{}, // No explicit disable - use upstream default
		},
		{
			name:           "WebhookProviderOpenshiftServiceCA enabled",
			downstreamGate: features.FeatureGateNewOLMWebhookProviderOpenshiftServiceCA,
			enabled:        true,
			expectedValues: map[string]interface{}{
				"options": map[string]interface{}{
					"operatorController": map[string]interface{}{
						"features": map[string]interface{}{
							"enabled":  []interface{}{WebhookProviderOpenshiftServiceCA},
							"disabled": []interface{}{WebhookProviderCertManager},
						},
					},
				},
			},
		},
		{
			name:           "WebhookProviderOpenshiftServiceCA disabled",
			downstreamGate: features.FeatureGateNewOLMWebhookProviderOpenshiftServiceCA,
			enabled:        false,
			expectedValues: map[string]interface{}{
				"options": map[string]interface{}{
					"operatorController": map[string]interface{}{
						"features": map[string]interface{}{
							"disabled": []interface{}{WebhookProviderCertManager}, // Only CertManager is explicitly disabled
						},
					},
				},
			},
		},
		{
			name:           "CatalogdAPIV1Metas enabled",
			downstreamGate: features.FeatureGateNewOLMCatalogdAPIV1Metas,
			enabled:        true,
			expectedValues: map[string]interface{}{
				"options": map[string]interface{}{
					"catalogd": map[string]interface{}{
						"features": map[string]interface{}{
							"enabled": []interface{}{APIV1MetasHandler},
						},
					},
				},
			},
		},
		{
			name:           "CatalogdAPIV1Metas disabled",
			downstreamGate: features.FeatureGateNewOLMCatalogdAPIV1Metas,
			enabled:        false,
			expectedValues: map[string]interface{}{}, // No explicit disable - use upstream default
		},
	}

	mapper := NewMapper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hv := helmvalues.NewHelmValues()
			fn := mapper.UpstreamForDownstream(tt.downstreamGate)
			if fn == nil {
				t.Fatalf("No mapping function found for gate %s", tt.downstreamGate)
			}

			err := fn(hv, tt.enabled)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			actual := hv.GetValues()
			if !reflect.DeepEqual(actual, tt.expectedValues) {
				t.Errorf("Expected values %v, got %v", tt.expectedValues, actual)
			}
		})
	}
}

func TestMapper_FeatureGateValidation(t *testing.T) {
	t.Run("all feature gates have proper NewOLM prefix", func(t *testing.T) {
		mapper := NewMapper()
		gates := mapper.DownstreamFeatureGates()

		for _, gate := range gates {
			gateStr := string(gate)
			if !strings.HasPrefix(gateStr, string(features.FeatureGateNewOLM)) {
				t.Errorf("Feature gate %s does not have NewOLM prefix", gate)
			}
			if gate == features.FeatureGateNewOLM {
				t.Errorf("Feature gate should not be FeatureGateNewOLM directly: %s", gate)
			}
		}
	})

	t.Run("validates constants are not empty", func(t *testing.T) {
		constants := []string{
			APIV1MetasHandler,
			PreflightPermissions,
			SingleOwnNamespaceInstallSupport,
			WebhookProviderOpenshiftServiceCA,
			WebhookProviderCertManager,
		}

		for i, constant := range constants {
			if constant == "" {
				t.Errorf("Constant at index %d is empty", i)
			}
		}
	})
}

func TestMapper_Constants(t *testing.T) {
	expectedConstants := map[string]string{
		"APIV1MetasHandler":                 APIV1MetasHandler,
		"PreflightPermissions":              PreflightPermissions,
		"SingleOwnNamespaceInstallSupport":  SingleOwnNamespaceInstallSupport,
		"WebhookProviderOpenshiftServiceCA": WebhookProviderOpenshiftServiceCA,
		"WebhookProviderCertManager":        WebhookProviderCertManager,
	}

	for name, constant := range expectedConstants {
		if constant == "" {
			t.Errorf("Constant %s is empty", name)
		}
	}

	if APIV1MetasHandler == PreflightPermissions {
		t.Error("APIV1MetasHandler and PreflightPermissions should be different")
	}
}
