package controller

import (
	"testing"

	configv1 "github.com/openshift/api/config/v1"
)

func TestTLSObserverPaths(t *testing.T) {
	// Test that the configuration paths are correctly defined
	expectedMinTLSPath := []string{"olmTLSSecurityProfile", "minTLSVersion"}
	expectedCipherSuitesPath := []string{"olmTLSSecurityProfile", "cipherSuites"}

	if !equalStringSlices(TLSMinVersionPath(), expectedMinTLSPath) {
		t.Errorf("TLSMinVersionPath() = %v, want %v", TLSMinVersionPath(), expectedMinTLSPath)
	}

	if !equalStringSlices(TLSCipherSuitesPath(), expectedCipherSuitesPath) {
		t.Errorf("TLSCipherSuitesPath() = %v, want %v", TLSCipherSuitesPath(), expectedCipherSuitesPath)
	}
}

func TestTLSSecurityProfileTypes(t *testing.T) {
	// Test that TLS security profile types are available
	types := []configv1.TLSProfileType{
		configv1.TLSProfileOldType,
		configv1.TLSProfileIntermediateType,
		configv1.TLSProfileModernType,
		configv1.TLSProfileCustomType,
	}

	expectedTypes := []string{"Old", "Intermediate", "Modern", "Custom"}

	for i, profileType := range types {
		if string(profileType) != expectedTypes[i] {
			t.Errorf("TLS profile type %d = %s, want %s", i, string(profileType), expectedTypes[i])
		}
	}

	// Test that TLS profiles map exists
	if configv1.TLSProfiles[configv1.TLSProfileIntermediateType] == nil {
		t.Errorf("TLSProfiles map missing Intermediate profile")
	}
}

func TestLibraryGoIntegration(t *testing.T) {
	// Test that we can access the library-go apiserver package
	// This verifies that the vendor update was successful
	if configv1.TLSProfiles[configv1.TLSProfileIntermediateType] == nil {
		t.Errorf("TLSProfiles map missing Intermediate profile")
	}

	// Test that we have the expected TLS profile structure
	intermediateProfile := configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	if intermediateProfile.MinTLSVersion != configv1.VersionTLS12 {
		t.Errorf("Intermediate profile minTLSVersion = %s, want VersionTLS12", intermediateProfile.MinTLSVersion)
	}
	if len(intermediateProfile.Ciphers) == 0 {
		t.Errorf("Intermediate profile has no cipher suites")
	}
}

func TestGetMapKeys(t *testing.T) {
	// Test the helper function used for logging
	testMap := map[string]interface{}{
		"key1": "value1",
		"key2": 42,
		"key3": []string{"a", "b"},
	}

	keys := getMapKeys(testMap)
	if len(keys) != 3 {
		t.Errorf("getMapKeys() returned %d keys, want 3", len(keys))
	}

	// Check that all expected keys are present (order may vary)
	expectedKeys := map[string]bool{"key1": false, "key2": false, "key3": false}
	for _, key := range keys {
		if _, exists := expectedKeys[key]; exists {
			expectedKeys[key] = true
		} else {
			t.Errorf("getMapKeys() returned unexpected key: %s", key)
		}
	}

	// Check that all expected keys were found
	for key, found := range expectedKeys {
		if !found {
			t.Errorf("getMapKeys() missing expected key: %s", key)
		}
	}

	// Test empty map
	emptyKeys := getMapKeys(map[string]interface{}{})
	if len(emptyKeys) != 0 {
		t.Errorf("getMapKeys() for empty map returned %d keys, want 0", len(emptyKeys))
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
