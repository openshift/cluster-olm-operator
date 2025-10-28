package clients

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	operatorv1 "github.com/openshift/api/operator/v1"
	operatorv1apply "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestGenerateOperatorSpecPatch(t *testing.T) {
	// Create a test OperatorSpec with all fields populated
	testSpec := &operatorv1.OperatorSpec{
		ManagementState:            operatorv1.Managed,
		LogLevel:                   operatorv1.Debug,
		OperatorLogLevel:           operatorv1.Normal,
		UnsupportedConfigOverrides: runtime.RawExtension{Raw: []byte(`{"test":"value"}`)},
		ObservedConfig:             runtime.RawExtension{Raw: []byte(`{"olmTLSSecurityProfile":{"minTLSVersion":"VersionTLS12","cipherSuites":["TLS_AES_128_GCM_SHA256"]}}`)},
	}

	// Generate the patch
	patchBytes, err := generateOperatorSpecPatch("test-version", testSpec)
	if err != nil {
		t.Fatalf("Error generating patch: %v", err)
	}

	// Unmarshal the patch to verify its structure
	var patchObj map[string]interface{}
	if err := json.Unmarshal(patchBytes, &patchObj); err != nil {
		t.Fatalf("Error unmarshaling patch: %v", err)
	}

	// Verify top-level patch structure
	if apiVersion, ok := patchObj["apiVersion"].(string); !ok || apiVersion != "operator.openshift.io/v1" {
		t.Errorf("Expected apiVersion 'operator.openshift.io/v1', got %v", patchObj["apiVersion"])
	}

	if kind, ok := patchObj["kind"].(string); !ok || kind != "OLM" {
		t.Errorf("Expected kind 'OLM', got %v", patchObj["kind"])
	}

	// Verify that metadata field is NOT present (to avoid conflicts with finalizer patches)
	if _, hasMetadata := patchObj["metadata"]; hasMetadata {
		t.Error("OperatorSpec patch should not contain metadata field to avoid conflicting with finalizer patches")
	}

	// Verify spec field exists
	spec, ok := patchObj["spec"].(map[string]interface{})
	if !ok {
		t.Fatal("No spec field in patch")
	}

	// Verify all OperatorSpec fields are present and correct
	expectedFields := map[string]interface{}{
		"managementState":  string(operatorv1.Managed),
		"logLevel":         string(operatorv1.Debug),
		"operatorLogLevel": string(operatorv1.Normal),
	}

	for field, expectedValue := range expectedFields {
		if actualValue, exists := spec[field]; !exists {
			t.Errorf("Expected field %s missing from patch", field)
		} else if actualValue != expectedValue {
			t.Errorf("Field %s: expected %v, got %v", field, expectedValue, actualValue)
		}
	}

	// Verify complex fields exist (we don't need to verify their exact content, just presence)
	complexFields := []string{"unsupportedConfigOverrides", "observedConfig"}
	for _, field := range complexFields {
		if _, exists := spec[field]; !exists {
			t.Errorf("Expected field %s missing from patch", field)
		}
	}

	// Verify that the patch only contains expected OperatorSpec fields
	expectedFieldCount := 5 // managementState, logLevel, operatorLogLevel, unsupportedConfigOverrides, observedConfig
	if len(spec) != expectedFieldCount {
		t.Errorf("Expected %d fields in spec, got %d. Fields: %v", expectedFieldCount, len(spec), getKeys(spec))
	}

	// Verify no extra fields that shouldn't be there
	for field := range spec {
		switch field {
		case "managementState", "logLevel", "operatorLogLevel", "unsupportedConfigOverrides", "observedConfig":
			// These are expected OperatorSpec fields
		default:
			t.Errorf("Unexpected field %s in patch spec", field)
		}
	}
}

func TestGenerateOperatorSpecPatchWithMinimalSpec(t *testing.T) {
	// Test with minimal OperatorSpec (only required fields)
	testSpec := &operatorv1.OperatorSpec{
		ManagementState: operatorv1.Managed,
		// Other fields are optional and may be empty
	}

	patchBytes, err := generateOperatorSpecPatch("minimal-version", testSpec)
	if err != nil {
		t.Fatalf("Error generating patch: %v", err)
	}

	var patchObj map[string]interface{}
	if err := json.Unmarshal(patchBytes, &patchObj); err != nil {
		t.Fatalf("Error unmarshaling patch: %v", err)
	}

	spec, ok := patchObj["spec"].(map[string]interface{})
	if !ok {
		t.Fatal("No spec field in patch")
	}

	// ManagementState should always be present
	if managementState, exists := spec["managementState"]; !exists {
		t.Error("managementState field missing from patch")
	} else if managementState != string(operatorv1.Managed) {
		t.Errorf("managementState: expected %s, got %v", operatorv1.Managed, managementState)
	}

	// All OperatorSpec fields should be present (even if empty/nil)
	expectedFields := []string{"managementState", "logLevel", "operatorLogLevel", "unsupportedConfigOverrides", "observedConfig"}
	for _, field := range expectedFields {
		if _, exists := spec[field]; !exists {
			t.Errorf("Expected field %s missing from patch", field)
		}
	}
}

func TestGenerateOperatorSpecPatchPreservesOLMFields(t *testing.T) {
	// This test verifies that the patch function only includes OperatorSpec fields
	// and would not overwrite hypothetical OLM-specific fields

	testSpec := &operatorv1.OperatorSpec{
		ManagementState: operatorv1.Managed,
		ObservedConfig:  runtime.RawExtension{Raw: []byte(`{"olmTLSSecurityProfile":{"minTLSVersion":"VersionTLS12"}}`)},
	}

	patchBytes, err := generateOperatorSpecPatch("preserve-test", testSpec)
	if err != nil {
		t.Fatalf("Error generating patch: %v", err)
	}

	var patchObj map[string]interface{}
	if err := json.Unmarshal(patchBytes, &patchObj); err != nil {
		t.Fatalf("Error unmarshaling patch: %v", err)
	}

	// Verify that the patch only targets the "spec" field at the top level
	// This ensures it won't overwrite other OLM-specific fields that might exist
	expectedTopLevelFields := []string{"apiVersion", "kind", "spec"}
	for _, field := range expectedTopLevelFields {
		if _, exists := patchObj[field]; !exists {
			t.Errorf("Expected top-level field %s missing from patch", field)
		}
	}

	// Verify no unexpected top-level fields (especially no metadata to avoid conflicts)
	for field := range patchObj {
		found := false
		for _, expected := range expectedTopLevelFields {
			if field == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Unexpected top-level field %s in patch", field)
		}
	}

	// Specifically verify metadata is NOT present (to avoid conflicting with finalizer patches)
	if _, hasMetadata := patchObj["metadata"]; hasMetadata {
		t.Error("OperatorSpec patch must not contain metadata field to avoid conflicting with finalizer patches")
	}
}

func TestGenerateOLMPatch(t *testing.T) {
	// Test generateOLMPatch with status data
	testStatus := &operatorv1.OperatorStatus{
		Conditions: []operatorv1.OperatorCondition{
			{
				Type:    "Available",
				Status:  operatorv1.ConditionTrue,
				Reason:  "AsExpected",
				Message: "TLS observer is working correctly",
			},
		},
		Version: "4.17.0",
	}

	patchBytes, err := generateOLMPatch("status-test-version", testStatus, "status")
	if err != nil {
		t.Fatalf("generateOLMPatch failed: %v", err)
	}

	// Verify the patch structure
	var patchObj map[string]interface{}
	if err := json.Unmarshal(patchBytes, &patchObj); err != nil {
		t.Fatalf("Failed to unmarshal patch: %v", err)
	}

	// Verify top-level structure
	if apiVersion, ok := patchObj["apiVersion"].(string); !ok || apiVersion != "operator.openshift.io/v1" {
		t.Errorf("Expected apiVersion 'operator.openshift.io/v1', got %v", patchObj["apiVersion"])
	}

	if kind, ok := patchObj["kind"].(string); !ok || kind != "OLM" {
		t.Errorf("Expected kind 'OLM', got %v", patchObj["kind"])
	}

	// Verify status field exists
	status, ok := patchObj["status"].(map[string]interface{})
	if !ok {
		t.Fatal("No status field in patch")
	}

	// Verify conditions exist
	conditions, ok := status["conditions"].([]interface{})
	if !ok {
		t.Fatal("No conditions field in status")
	}

	if len(conditions) != 1 {
		t.Errorf("Expected 1 condition, got %d", len(conditions))
	}

	// Verify condition content
	condition := conditions[0].(map[string]interface{})
	if condition["type"] != "Available" {
		t.Errorf("Expected condition type 'Available', got %v", condition["type"])
	}
	if condition["status"] != "True" {
		t.Errorf("Expected condition status 'True', got %v", condition["status"])
	}
}

func TestGenerateOLMPatchWithObservedConfig(t *testing.T) {
	// Test generateOLMPatch with observedConfig-like data (simulating what config observers would do)
	testConfig := map[string]interface{}{
		"olmTLSSecurityProfile": map[string]interface{}{
			"minTLSVersion": "VersionTLS12",
			"cipherSuites":  []string{"TLS_AES_128_GCM_SHA256", "TLS_AES_256_GCM_SHA384"},
		},
	}

	patchBytes, err := generateOLMPatch("config-test-version", testConfig, "spec", "observedConfig")
	if err != nil {
		t.Fatalf("generateOLMPatch failed: %v", err)
	}

	// Verify the patch structure
	var patchObj map[string]interface{}
	if err := json.Unmarshal(patchBytes, &patchObj); err != nil {
		t.Fatalf("Failed to unmarshal patch: %v", err)
	}

	// Navigate to nested observedConfig
	spec, ok := patchObj["spec"].(map[string]interface{})
	if !ok {
		t.Fatal("No spec field in patch")
	}

	observedConfig, ok := spec["observedConfig"].(map[string]interface{})
	if !ok {
		t.Fatal("No observedConfig field in spec")
	}

	// Verify TLS profile
	tlsProfile, ok := observedConfig["olmTLSSecurityProfile"].(map[string]interface{})
	if !ok {
		t.Fatal("No olmTLSSecurityProfile in observedConfig")
	}

	if tlsProfile["minTLSVersion"] != "VersionTLS12" {
		t.Errorf("Expected minTLSVersion 'VersionTLS12', got %v", tlsProfile["minTLSVersion"])
	}

	cipherSuites, ok := tlsProfile["cipherSuites"].([]interface{})
	if !ok || len(cipherSuites) != 2 {
		t.Errorf("Expected 2 cipher suites, got %v", cipherSuites)
	}
}

func TestGenerateOLMPatchErrorHandling(t *testing.T) {
	// Test with invalid input that should trigger error logging
	invalidInput := make(chan int) // channels can't be marshaled

	_, err := generateOLMPatch("error-test-version", invalidInput, "spec")
	if err == nil {
		t.Error("Expected error for invalid input, but got none")
	}

	// The error should contain information about conversion failure
	if !strings.Contains(err.Error(), "error converting to unstructured") {
		t.Errorf("Expected error about conversion failure, got: %v", err)
	}
}

func TestApplyOperatorSpecLogging(t *testing.T) {
	// This test demonstrates the logging functionality of ApplyOperatorSpec
	// Note: This creates a mock scenario to test logging without requiring a real K8s cluster

	// Test that ApplyOperatorSpec handles nil configuration correctly with logging
	client := &OperatorClient{} // Mock client for testing

	err := client.ApplyOperatorSpec(context.Background(), "test-field-manager", nil)
	if err == nil {
		t.Error("Expected error for nil applyConfiguration, but got none")
	}

	// The error should contain the expected message
	expectedError := "applyConfiguration must have a value"
	if !strings.Contains(err.Error(), expectedError) {
		t.Errorf("Expected error containing %q, got: %v", expectedError, err)
	}
}

func TestPatchOperatorStatusLogging(t *testing.T) {
	// This test demonstrates the logging functionality of PatchOperatorStatus
	// Note: This creates a mock scenario to test logging without requiring a real K8s cluster

	// Test that PatchOperatorStatus handles nil jsonPatch correctly with logging
	client := &OperatorClient{} // Mock client for testing

	err := client.PatchOperatorStatus(context.Background(), nil)
	if err == nil {
		t.Error("Expected error for nil jsonPatch, but got none")
	}

	// The error should contain the expected message
	expectedError := "jsonPatch must have a value"
	if !strings.Contains(err.Error(), expectedError) {
		t.Errorf("Expected error containing %q, got: %v", expectedError, err)
	}
}

func TestConvertApplyConfigToOperatorSpec(t *testing.T) {
	// Test the conversion from OperatorSpecApplyConfiguration to OperatorSpec
	applyConfig := &operatorv1apply.OperatorSpecApplyConfiguration{}
	applyConfig.WithManagementState(operatorv1.Managed)
	applyConfig.WithLogLevel(operatorv1.Debug)
	applyConfig.WithOperatorLogLevel(operatorv1.Normal)

	operatorSpec, err := convertApplyConfigToOperatorSpec(applyConfig)
	if err != nil {
		t.Fatalf("convertApplyConfigToOperatorSpec failed: %v", err)
	}

	if operatorSpec.ManagementState != operatorv1.Managed {
		t.Errorf("Expected ManagementState %s, got %s", operatorv1.Managed, operatorSpec.ManagementState)
	}

	if operatorSpec.LogLevel != operatorv1.Debug {
		t.Errorf("Expected LogLevel %s, got %s", operatorv1.Debug, operatorSpec.LogLevel)
	}

	if operatorSpec.OperatorLogLevel != operatorv1.Normal {
		t.Errorf("Expected OperatorLogLevel %s, got %s", operatorv1.Normal, operatorSpec.OperatorLogLevel)
	}
}

func TestOperatorSpecNeedsUpdate(t *testing.T) {
	// Test the comparison function
	baseSpec := operatorv1.OperatorSpec{
		ManagementState:  operatorv1.Managed,
		LogLevel:         operatorv1.Debug,
		OperatorLogLevel: operatorv1.Normal,
	}

	// Test no changes needed
	needsUpdate, err := operatorSpecNeedsUpdate(baseSpec, baseSpec)
	if err != nil {
		t.Fatalf("operatorSpecNeedsUpdate failed: %v", err)
	}
	if needsUpdate {
		t.Error("Expected no update needed for identical specs")
	}

	// Test management state change
	changedSpec := baseSpec
	changedSpec.ManagementState = operatorv1.Unmanaged
	needsUpdate, err = operatorSpecNeedsUpdate(baseSpec, changedSpec)
	if err != nil {
		t.Fatalf("operatorSpecNeedsUpdate failed: %v", err)
	}
	if !needsUpdate {
		t.Error("Expected update needed for different ManagementState")
	}

	// Test log level change
	changedSpec = baseSpec
	changedSpec.LogLevel = operatorv1.Trace
	needsUpdate, err = operatorSpecNeedsUpdate(baseSpec, changedSpec)
	if err != nil {
		t.Fatalf("operatorSpecNeedsUpdate failed: %v", err)
	}
	if !needsUpdate {
		t.Error("Expected update needed for different LogLevel")
	}
}

func TestGenerateFinalizerPatch(t *testing.T) {
	// Test generateFinalizerPatch function
	testFinalizers := []string{"finalizer1", "finalizer2"}

	patchBytes, err := generateFinalizerPatch("test-version", testFinalizers)
	if err != nil {
		t.Fatalf("Error generating finalizer patch: %v", err)
	}

	// Unmarshal the patch to verify its structure
	var patchObj map[string]interface{}
	if err := json.Unmarshal(patchBytes, &patchObj); err != nil {
		t.Fatalf("Error unmarshaling finalizer patch: %v", err)
	}

	// Verify top-level patch structure
	if apiVersion, ok := patchObj["apiVersion"].(string); !ok || apiVersion != "operator.openshift.io/v1" {
		t.Errorf("Expected apiVersion 'operator.openshift.io/v1', got %v", patchObj["apiVersion"])
	}

	if kind, ok := patchObj["kind"].(string); !ok || kind != "OLM" {
		t.Errorf("Expected kind 'OLM', got %v", patchObj["kind"])
	}

	if resourceVersion, ok := patchObj["metadata"].(map[string]interface{})["resourceVersion"].(string); !ok || resourceVersion != "test-version" {
		t.Errorf("Expected resourceVersion 'test-version', got %v", resourceVersion)
	}

	// Verify finalizers field exists in metadata
	metadata, ok := patchObj["metadata"].(map[string]interface{})
	if !ok {
		t.Fatal("No metadata field in patch")
	}

	finalizers, ok := metadata["finalizers"].([]interface{})
	if !ok {
		t.Fatal("No finalizers field in metadata")
	}

	if len(finalizers) != 2 {
		t.Errorf("Expected 2 finalizers, got %d", len(finalizers))
	}

	// Verify finalizer content
	if finalizers[0] != "finalizer1" || finalizers[1] != "finalizer2" {
		t.Errorf("Expected finalizers [finalizer1, finalizer2], got %v", finalizers)
	}

	// Verify that ONLY metadata.finalizers is set (no spec field should exist)
	if _, hasSpec := patchObj["spec"]; hasSpec {
		t.Error("Finalizer patch should not contain spec field")
	}

	if _, hasStatus := patchObj["status"]; hasStatus {
		t.Error("Finalizer patch should not contain status field")
	}

	// Verify metadata only contains resourceVersion and finalizers
	expectedMetadataFields := map[string]bool{"resourceVersion": false, "finalizers": false}
	for field := range metadata {
		if _, expected := expectedMetadataFields[field]; expected {
			expectedMetadataFields[field] = true
		} else {
			t.Errorf("Unexpected metadata field %s in finalizer patch", field)
		}
	}

	// Check that all expected metadata fields were found
	for field, found := range expectedMetadataFields {
		if !found {
			t.Errorf("Missing expected metadata field: %s", field)
		}
	}
}

func TestGenerateFinalizerPatchWithEmptyFinalizers(t *testing.T) {
	// Test with empty finalizers list
	patchBytes, err := generateFinalizerPatch("empty-test", []string{})
	if err != nil {
		t.Fatalf("Error generating empty finalizer patch: %v", err)
	}

	var patchObj map[string]interface{}
	if err := json.Unmarshal(patchBytes, &patchObj); err != nil {
		t.Fatalf("Error unmarshaling empty finalizer patch: %v", err)
	}

	metadata, ok := patchObj["metadata"].(map[string]interface{})
	if !ok {
		t.Fatal("No metadata field in patch")
	}

	finalizers, ok := metadata["finalizers"].([]interface{})
	if !ok {
		t.Fatal("No finalizers field in metadata")
	}

	if len(finalizers) != 0 {
		t.Errorf("Expected 0 finalizers, got %d", len(finalizers))
	}
}

func TestGenerateFinalizerPatchPreservesOtherFields(t *testing.T) {
	// This test verifies that the finalizer patch only targets metadata.finalizers
	// and would not overwrite other fields like spec when applied

	testFinalizers := []string{"test-finalizer"}

	patchBytes, err := generateFinalizerPatch("preserve-test", testFinalizers)
	if err != nil {
		t.Fatalf("Error generating finalizer patch: %v", err)
	}

	var patchObj map[string]interface{}
	if err := json.Unmarshal(patchBytes, &patchObj); err != nil {
		t.Fatalf("Error unmarshaling finalizer patch: %v", err)
	}

	// Verify that the patch only targets specific fields and won't overwrite others
	expectedTopLevelFields := []string{"apiVersion", "kind", "metadata"}
	for _, field := range expectedTopLevelFields {
		if _, exists := patchObj[field]; !exists {
			t.Errorf("Expected top-level field %s missing from finalizer patch", field)
		}
	}

	// Verify no unexpected top-level fields that would overwrite OLM fields
	for field := range patchObj {
		found := false
		for _, expected := range expectedTopLevelFields {
			if field == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Unexpected top-level field %s in finalizer patch", field)
		}
	}

	// Specifically verify spec and status are NOT present
	if _, hasSpec := patchObj["spec"]; hasSpec {
		t.Error("Finalizer patch must not contain spec field to avoid overwriting OperatorSpec fields")
	}
	if _, hasStatus := patchObj["status"]; hasStatus {
		t.Error("Finalizer patch must not contain status field to avoid overwriting OperatorStatus fields")
	}
}

// Helper function to get map keys for debugging
func getKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
