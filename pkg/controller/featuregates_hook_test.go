package controller

import (
	"errors"
	"reflect"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"

	"github.com/openshift/cluster-olm-operator/pkg/helmvalues"
)

// mockFeatureGate implements featuregates.FeatureGate for testing
type mockFeatureGate struct {
	enabledGates  []configv1.FeatureGateName
	disabledGates []configv1.FeatureGateName
}

func newMockFeatureGate(enabled, disabled []configv1.FeatureGateName) *mockFeatureGate {
	return &mockFeatureGate{
		enabledGates:  enabled,
		disabledGates: disabled,
	}
}

func (m *mockFeatureGate) Enabled(gate configv1.FeatureGateName) bool {
	for _, enabledGate := range m.enabledGates {
		if enabledGate == gate {
			return true
		}
	}
	return false
}

func (m *mockFeatureGate) KnownFeatures() []configv1.FeatureGateName {
	all := make([]configv1.FeatureGateName, 0, len(m.enabledGates)+len(m.disabledGates))
	all = append(all, m.enabledGates...)
	all = append(all, m.disabledGates...)
	return all
}

func TestUpstreamFeatureGates(t *testing.T) {
	tests := []struct {
		name                     string
		clusterGatesConfig       featuregates.FeatureGate
		downstreamGates          []configv1.FeatureGateName
		downstreamToUpstreamFunc func(configv1.FeatureGateName) func(*helmvalues.HelmValues, bool) error
		expectedValues           map[string]interface{}
		expectError              bool
	}{
		{
			name: "empty downstream gates",
			clusterGatesConfig: newMockFeatureGate(
				[]configv1.FeatureGateName{},
				[]configv1.FeatureGateName{},
			),
			downstreamGates: []configv1.FeatureGateName{},
			downstreamToUpstreamFunc: func(_ configv1.FeatureGateName) func(*helmvalues.HelmValues, bool) error {
				return func(_ *helmvalues.HelmValues, _ bool) error {
					return nil
				}
			},
			expectedValues: map[string]interface{}{},
			expectError:    false,
		},
		{
			name: "single enabled feature gate",
			clusterGatesConfig: newMockFeatureGate(
				[]configv1.FeatureGateName{features.FeatureGateNewOLMPreflightPermissionChecks},
				[]configv1.FeatureGateName{},
			),
			downstreamGates: []configv1.FeatureGateName{features.FeatureGateNewOLMPreflightPermissionChecks},
			downstreamToUpstreamFunc: func(gate configv1.FeatureGateName) func(*helmvalues.HelmValues, bool) error {
				if gate == features.FeatureGateNewOLMPreflightPermissionChecks {
					return func(hv *helmvalues.HelmValues, enabled bool) error {
						if enabled {
							return hv.AddListValue("options.operatorController.features.enabled", "PreflightPermissions")
						}
						return hv.AddListValue("options.operatorController.features.disabled", "PreflightPermissions")
					}
				}
				return func(_ *helmvalues.HelmValues, _ bool) error {
					return nil
				}
			},
			expectedValues: map[string]interface{}{
				"options": map[string]interface{}{
					"operatorController": map[string]interface{}{
						"features": map[string]interface{}{
							"enabled": []interface{}{"PreflightPermissions"},
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "single disabled feature gate",
			clusterGatesConfig: newMockFeatureGate(
				[]configv1.FeatureGateName{},
				[]configv1.FeatureGateName{features.FeatureGateNewOLMPreflightPermissionChecks},
			),
			downstreamGates: []configv1.FeatureGateName{features.FeatureGateNewOLMPreflightPermissionChecks},
			downstreamToUpstreamFunc: func(gate configv1.FeatureGateName) func(*helmvalues.HelmValues, bool) error {
				if gate == features.FeatureGateNewOLMPreflightPermissionChecks {
					return func(hv *helmvalues.HelmValues, enabled bool) error {
						if enabled {
							return hv.AddListValue("options.operatorController.features.enabled", "PreflightPermissions")
						}
						return hv.AddListValue("options.operatorController.features.disabled", "PreflightPermissions")
					}
				}
				return func(_ *helmvalues.HelmValues, _ bool) error {
					return nil
				}
			},
			expectedValues: map[string]interface{}{
				"options": map[string]interface{}{
					"operatorController": map[string]interface{}{
						"features": map[string]interface{}{
							"disabled": []interface{}{"PreflightPermissions"},
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "multiple feature gates mixed enabled/disabled",
			clusterGatesConfig: newMockFeatureGate(
				[]configv1.FeatureGateName{
					features.FeatureGateNewOLMPreflightPermissionChecks,
					features.FeatureGateNewOLMOwnSingleNamespace,
				},
				[]configv1.FeatureGateName{
					features.FeatureGateNewOLMWebhookProviderOpenshiftServiceCA,
				},
			),
			downstreamGates: []configv1.FeatureGateName{
				features.FeatureGateNewOLMPreflightPermissionChecks,
				features.FeatureGateNewOLMOwnSingleNamespace,
				features.FeatureGateNewOLMWebhookProviderOpenshiftServiceCA,
			},
			downstreamToUpstreamFunc: func(gate configv1.FeatureGateName) func(*helmvalues.HelmValues, bool) error {
				switch gate {
				case features.FeatureGateNewOLMPreflightPermissionChecks:
					return func(hv *helmvalues.HelmValues, enabled bool) error {
						if enabled {
							return hv.AddListValue("options.operatorController.features.enabled", "PreflightPermissions")
						}
						return hv.AddListValue("options.operatorController.features.disabled", "PreflightPermissions")
					}
				case features.FeatureGateNewOLMOwnSingleNamespace:
					return func(hv *helmvalues.HelmValues, enabled bool) error {
						if enabled {
							return hv.AddListValue("options.operatorController.features.enabled", "SingleOwnNamespaceInstallSupport")
						}
						return hv.AddListValue("options.operatorController.features.disabled", "SingleOwnNamespaceInstallSupport")
					}
				case features.FeatureGateNewOLMWebhookProviderOpenshiftServiceCA:
					return func(hv *helmvalues.HelmValues, enabled bool) error {
						if enabled {
							return hv.AddListValue("options.operatorController.features.enabled", "WebhookProviderOpenshiftServiceCA")
						}
						return hv.AddListValue("options.operatorController.features.disabled", "WebhookProviderOpenshiftServiceCA")
					}
				}
				return func(_ *helmvalues.HelmValues, _ bool) error {
					return nil
				}
			},
			expectedValues: map[string]interface{}{
				"options": map[string]interface{}{
					"operatorController": map[string]interface{}{
						"features": map[string]interface{}{
							"enabled":  []interface{}{"PreflightPermissions", "SingleOwnNamespaceInstallSupport"},
							"disabled": []interface{}{"WebhookProviderOpenshiftServiceCA"},
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "mapping function returns error",
			clusterGatesConfig: newMockFeatureGate(
				[]configv1.FeatureGateName{features.FeatureGateNewOLMPreflightPermissionChecks},
				[]configv1.FeatureGateName{},
			),
			downstreamGates: []configv1.FeatureGateName{features.FeatureGateNewOLMPreflightPermissionChecks},
			downstreamToUpstreamFunc: func(_ configv1.FeatureGateName) func(*helmvalues.HelmValues, bool) error {
				return func(_ *helmvalues.HelmValues, _ bool) error {
					return errors.New("mapping function error")
				}
			},
			expectedValues: map[string]interface{}{},
			expectError:    true,
		},
		{
			name: "multiple mapping function errors",
			clusterGatesConfig: newMockFeatureGate(
				[]configv1.FeatureGateName{
					features.FeatureGateNewOLMPreflightPermissionChecks,
					features.FeatureGateNewOLMOwnSingleNamespace,
				},
				[]configv1.FeatureGateName{},
			),
			downstreamGates: []configv1.FeatureGateName{
				features.FeatureGateNewOLMPreflightPermissionChecks,
				features.FeatureGateNewOLMOwnSingleNamespace,
			},
			downstreamToUpstreamFunc: func(gate configv1.FeatureGateName) func(*helmvalues.HelmValues, bool) error {
				return func(_ *helmvalues.HelmValues, _ bool) error {
					return errors.New("mapping error for " + string(gate))
				}
			},
			expectedValues: map[string]interface{}{},
			expectError:    true,
		},
		{
			name: "unknown downstream gate",
			clusterGatesConfig: newMockFeatureGate(
				[]configv1.FeatureGateName{},
				[]configv1.FeatureGateName{"UnknownGate"},
			),
			downstreamGates: []configv1.FeatureGateName{"UnknownGate"},
			downstreamToUpstreamFunc: func(_ configv1.FeatureGateName) func(*helmvalues.HelmValues, bool) error {
				// Return no-op function for unknown gates (no mapping available)
				return func(_ *helmvalues.HelmValues, _ bool) error {
					return nil
				}
			},
			expectedValues: map[string]interface{}{},
			expectError:    false,
		},
		{
			name: "complex webhook provider scenario",
			clusterGatesConfig: newMockFeatureGate(
				[]configv1.FeatureGateName{features.FeatureGateNewOLMWebhookProviderOpenshiftServiceCA},
				[]configv1.FeatureGateName{},
			),
			downstreamGates: []configv1.FeatureGateName{features.FeatureGateNewOLMWebhookProviderOpenshiftServiceCA},
			downstreamToUpstreamFunc: func(gate configv1.FeatureGateName) func(*helmvalues.HelmValues, bool) error {
				if gate == features.FeatureGateNewOLMWebhookProviderOpenshiftServiceCA {
					return func(hv *helmvalues.HelmValues, enabled bool) error {
						var errs []error
						if enabled {
							errs = append(errs, hv.AddListValue("options.operatorController.features.enabled", "WebhookProviderOpenshiftServiceCA"))
						} else {
							errs = append(errs, hv.AddListValue("options.operatorController.features.disabled", "WebhookProviderOpenshiftServiceCA"))
						}
						// Always disable cert manager
						errs = append(errs, hv.AddListValue("options.operatorController.features.disabled", "WebhookProviderCertManager"))
						return errors.Join(errs...)
					}
				}
				return func(_ *helmvalues.HelmValues, _ bool) error {
					return nil
				}
			},
			expectedValues: map[string]interface{}{
				"options": map[string]interface{}{
					"operatorController": map[string]interface{}{
						"features": map[string]interface{}{
							"enabled":  []interface{}{"WebhookProviderOpenshiftServiceCA"},
							"disabled": []interface{}{"WebhookProviderCertManager"},
						},
					},
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := upstreamFeatureGates(
				helmvalues.NewHelmValues(),
				tt.clusterGatesConfig,
				tt.downstreamGates,
				tt.downstreamToUpstreamFunc,
			)

			if tt.expectError && err == nil {
				t.Errorf("Expected error, but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if result == nil {
				t.Fatal("Result HelmValues is nil")
			}

			actualValues := result.GetValues()
			if !reflect.DeepEqual(actualValues, tt.expectedValues) {
				t.Errorf("Expected values %v, got %v", tt.expectedValues, actualValues)
			}
		})
	}
}

func TestUpstreamFeatureGates_NilMappingFunction(t *testing.T) {
	// Test case where the mapping function returns nil (no mapping for a gate)
	clusterGatesConfig := newMockFeatureGate(
		[]configv1.FeatureGateName{features.FeatureGateNewOLMPreflightPermissionChecks},
		[]configv1.FeatureGateName{},
	)
	downstreamGates := []configv1.FeatureGateName{features.FeatureGateNewOLMPreflightPermissionChecks}

	// This should panic when trying to call a nil function
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected panic when calling nil mapping function")
		}
	}()

	downstreamToUpstreamFunc := func(_ configv1.FeatureGateName) func(*helmvalues.HelmValues, bool) error {
		return nil // Return nil function
	}

	_, _ = upstreamFeatureGates(helmvalues.NewHelmValues(), clusterGatesConfig, downstreamGates, downstreamToUpstreamFunc)
}

func TestUpstreamFeatureGates_EdgeCases(t *testing.T) {
	// Test with no gates enabled/disabled but gates still present
	t.Run("gates present but none enabled", func(t *testing.T) {
		clusterGatesConfig := newMockFeatureGate(
			[]configv1.FeatureGateName{},
			[]configv1.FeatureGateName{},
		)
		downstreamGates := []configv1.FeatureGateName{features.FeatureGateNewOLMPreflightPermissionChecks}

		result, err := upstreamFeatureGates(
			helmvalues.NewHelmValues(),
			clusterGatesConfig,
			downstreamGates,
			func(_ configv1.FeatureGateName) func(*helmvalues.HelmValues, bool) error {
				return func(hv *helmvalues.HelmValues, enabled bool) error {
					if enabled {
						return hv.AddListValue("options.operatorController.features.enabled", "TestFeature")
					}
					return hv.AddListValue("options.operatorController.features.disabled", "TestFeature")
				}
			},
		)

		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}

		expectedValues := map[string]interface{}{
			"options": map[string]interface{}{
				"operatorController": map[string]interface{}{
					"features": map[string]interface{}{
						"disabled": []interface{}{"TestFeature"},
					},
				},
			},
		}

		if !reflect.DeepEqual(result.GetValues(), expectedValues) {
			t.Errorf("Expected values %v, got %v", expectedValues, result.GetValues())
		}
	})
}

func TestMockFeatureGate(t *testing.T) {
	// Test the mock implementation itself
	enabled := []configv1.FeatureGateName{
		features.FeatureGateNewOLMPreflightPermissionChecks,
		features.FeatureGateNewOLMOwnSingleNamespace,
	}
	disabled := []configv1.FeatureGateName{
		features.FeatureGateNewOLMWebhookProviderOpenshiftServiceCA,
	}

	mock := newMockFeatureGate(enabled, disabled)

	// Test Enabled method
	for _, gate := range enabled {
		if !mock.Enabled(gate) {
			t.Errorf("Expected gate %s to be enabled", gate)
		}
	}

	for _, gate := range disabled {
		if mock.Enabled(gate) {
			t.Errorf("Expected gate %s to be disabled", gate)
		}
	}

	// Test unknown gate
	if mock.Enabled("UnknownGate") {
		t.Errorf("Expected unknown gate to be disabled")
	}

	// Test KnownFeatures method
	known := mock.KnownFeatures()
	expectedCount := len(enabled) + len(disabled)
	if len(known) != expectedCount {
		t.Errorf("Expected %d known features, got %d", expectedCount, len(known))
	}

	// Verify all expected gates are in known features
	allExpected := append(enabled, disabled...)
	for _, expectedGate := range allExpected {
		found := false
		for _, knownGate := range known {
			if knownGate == expectedGate {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected gate %s not found in known features", expectedGate)
		}
	}
}
