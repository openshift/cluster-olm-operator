package featuregates

import (
	"slices"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
)

func TestMapper_ControllerUpstreamForDownstream(t *testing.T) {
	t.Run("returns mapped upstream gates for operator-controller", func(t *testing.T) {
		expectedUpstreamGates := []string{"HelloGate", "WorldGate"}

		mapper := NewMapper()
		mapper.operatorControllerGates = map[configv1.FeatureGateName][]string{
			features.FeatureGateNewOLM: expectedUpstreamGates,
		}
		upstream := mapper.OperatorControllerUpstreamForDownstream(features.FeatureGateNewOLM)
		if !slices.Equal(upstream, expectedUpstreamGates) {
			t.Fatalf("expected and returned upstream gates differ: upstream: %+v, expected: %+v",
				upstream, expectedUpstreamGates,
			)
		}
	})
}

func TestMapper_CatalogdUpstreamForDownstream(t *testing.T) {
	t.Run("returns mapped upstream gates for catalogd", func(t *testing.T) {
		expectedUpstreamGates := []string{"HelloGate", "WorldGate"}

		mapper := NewMapper()
		mapper.catalogdGates = map[configv1.FeatureGateName][]string{
			features.FeatureGateNewOLM: expectedUpstreamGates,
		}
		upstream := mapper.CatalogdUpstreamForDownstream(features.FeatureGateNewOLM)
		if !slices.Equal(upstream, expectedUpstreamGates) {
			t.Fatalf("expected and returned upstream gates differ: upstream: %+v, expected: %+v",
				upstream, expectedUpstreamGates,
			)
		}
	})
}

func TestFormatAsEnabledArgs(t *testing.T) {
	testCases := []struct {
		name     string
		in       []string
		expected string
	}{
		{
			name: "empty",
		},
		{
			name:     "single feature gate",
			in:       []string{"testGate1"},
			expected: "testGate1=true",
		},
		{
			name:     "multiple feature gates",
			in:       []string{"testGate1", "testGate2", "testGate3"},
			expected: "testGate1=true,testGate2=true,testGate2=true",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			result := FormatAsEnabledArgs(testCase.in)
			if result != testCase.expected {
				t.Fatalf("result and expected differ, expected: %q, got: %q", testCase.expected, result)
			}
		})
	}
}
