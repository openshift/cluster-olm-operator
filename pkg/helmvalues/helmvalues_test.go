package helmvalues

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNewHelmValues(t *testing.T) {
	hv := NewHelmValues()
	if hv == nil {
		t.Fatal("NewHelmValues returned nil")
	}
	if hv.values == nil {
		t.Errorf("Expected values to be initialized, got nil")
	}
	if len(hv.values) != 0 {
		t.Errorf("Expected empty values map, got %v", hv.values)
	}
}

func TestNewHelmValuesFromFiles(t *testing.T) {
	tests := []struct {
		name          string
		files         []string
		setupFiles    map[string]string
		expectedError bool
		expectedVals  map[string]interface{}
	}{
		{
			name:          "empty files list",
			files:         []string{},
			expectedError: false,
			expectedVals:  map[string]interface{}{},
		},
		{
			name:  "single valid yaml file",
			files: []string{"test1.yaml"},
			setupFiles: map[string]string{
				"test1.yaml": `
options:
  operatorController:
    features:
      enabled: ["feature1", "feature2"]
`,
			},
			expectedError: false,
			expectedVals: map[string]interface{}{
				"options": map[string]interface{}{
					"operatorController": map[string]interface{}{
						"features": map[string]interface{}{
							"enabled": []interface{}{"feature1", "feature2"},
						},
					},
				},
			},
		},
		{
			name:  "multiple yaml files with merge",
			files: []string{"test1.yaml", "test2.yaml"},
			setupFiles: map[string]string{
				"test1.yaml": `
options:
  operatorController:
    features:
      enabled: ["feature1"]
`,
				"test2.yaml": `
options:
  operatorController:
    features:
      disabled: ["feature2"]
`,
			},
			expectedError: false,
			expectedVals: map[string]interface{}{
				"options": map[string]interface{}{
					"operatorController": map[string]interface{}{
						"features": map[string]interface{}{
							"enabled":  []interface{}{"feature1"},
							"disabled": []interface{}{"feature2"},
						},
					},
				},
			},
		},
		{
			name:          "nonexistent file",
			files:         []string{"nonexistent.yaml"},
			expectedError: true,
		},
		{
			name:  "invalid yaml",
			files: []string{"invalid.yaml"},
			setupFiles: map[string]string{
				"invalid.yaml": `invalid: yaml: content: [`,
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			var filePaths []string

			for filename, content := range tt.setupFiles {
				path := filepath.Join(tmpDir, filename)
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatalf("Failed to create test file %s: %v", filename, err)
				}
				filePaths = append(filePaths, path)
			}

			if len(tt.files) > 0 && len(tt.setupFiles) == 0 {
				for _, f := range tt.files {
					filePaths = append(filePaths, filepath.Join(tmpDir, f))
				}
			}

			hv, err := NewHelmValuesFromFiles(filePaths)

			if tt.expectedError {
				if err == nil {
					t.Errorf("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if !reflect.DeepEqual(hv.values, tt.expectedVals) {
				t.Errorf("Expected values %v, got %v", tt.expectedVals, hv.values)
			}
		})
	}
}

func TestGetValues(t *testing.T) {
	hv := NewHelmValues()
	values := hv.GetValues()
	if len(values) != 0 {
		t.Errorf("Expected empty values, got %v", values)
	}

	hv.values = map[string]interface{}{
		"key": "value",
	}
	values = hv.GetValues()
	expected := map[string]interface{}{
		"key": "value",
	}
	if !reflect.DeepEqual(values, expected) {
		t.Errorf("Expected %v, got %v", expected, values)
	}
}

func TestHasEnabledFeatureGates(t *testing.T) {
	tests := []struct {
		name     string
		values   map[string]interface{}
		expected bool
		hasError bool
	}{
		{
			name:     "nil values",
			values:   nil,
			expected: false,
			hasError: false,
		},
		{
			name:     "empty values",
			values:   map[string]interface{}{},
			expected: false,
			hasError: false,
		},
		{
			name: "enabled operator controller features",
			values: map[string]interface{}{
				"options": map[string]interface{}{
					"operatorController": map[string]interface{}{
						"features": map[string]interface{}{
							"enabled": []interface{}{"feature1", "feature2"},
						},
					},
				},
			},
			expected: true,
			hasError: false,
		},
		{
			name: "empty enabled operator controller features",
			values: map[string]interface{}{
				"options": map[string]interface{}{
					"operatorController": map[string]interface{}{
						"features": map[string]interface{}{
							"enabled": []interface{}{},
						},
					},
				},
			},
			expected: false,
			hasError: false,
		},
		{
			name: "enabled catalogd features",
			values: map[string]interface{}{
				"options": map[string]interface{}{
					"operatorController": map[string]interface{}{
						"features": map[string]interface{}{
							"enabled": []interface{}{"catalogd-feature"},
						},
					},
				},
			},
			expected: true,
			hasError: false,
		},
		{
			name: "only disabled features",
			values: map[string]interface{}{
				"options": map[string]interface{}{
					"operatorController": map[string]interface{}{
						"features": map[string]interface{}{
							"disabled": []interface{}{"feature1"},
						},
					},
				},
			},
			expected: false,
			hasError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hv := NewHelmValues()
			if tt.values != nil {
				hv.values = tt.values
			}
			result, err := hv.HasEnabledFeatureGates()

			if tt.hasError && err == nil {
				t.Errorf("Expected error, got nil")
			}
			if !tt.hasError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestSetStringValue(t *testing.T) {
	tests := []struct {
		name        string
		location    string
		value       string
		expectError bool
		expectedVal map[string]interface{}
	}{
		{
			name:        "empty location",
			location:    "",
			value:       "test",
			expectError: true,
		},
		{
			name:        "simple location",
			location:    "key",
			value:       "value",
			expectError: false,
			expectedVal: map[string]interface{}{
				"key": "value",
			},
		},
		{
			name:        "nested location",
			location:    "options.operatorController.name",
			value:       "test-controller",
			expectError: false,
			expectedVal: map[string]interface{}{
				"options": map[string]interface{}{
					"operatorController": map[string]interface{}{
						"name": "test-controller",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hv := NewHelmValues()
			hv.values = make(map[string]interface{})

			err := hv.SetStringValue(tt.location, tt.value)

			if tt.expectError && err == nil {
				t.Errorf("Expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if !tt.expectError && !reflect.DeepEqual(hv.values, tt.expectedVal) {
				t.Errorf("Expected %v, got %v", tt.expectedVal, hv.values)
			}
		})
	}
}

func TestAddListValue(t *testing.T) {
	tests := []struct {
		name         string
		initialVals  map[string]interface{}
		location     string
		value        string
		expectError  bool
		expectedVals map[string]interface{}
	}{
		{
			name:        "empty location",
			location:    "",
			value:       "test",
			expectError: true,
		},
		{
			name:        "add to new location",
			initialVals: make(map[string]interface{}),
			location:    "options.features.enabled",
			value:       "feature1",
			expectError: false,
			expectedVals: map[string]interface{}{
				"options": map[string]interface{}{
					"features": map[string]interface{}{
						"enabled": []interface{}{"feature1"},
					},
				},
			},
		},
		{
			name: "add to existing list",
			initialVals: map[string]interface{}{
				"options": map[string]interface{}{
					"features": map[string]interface{}{
						"enabled": []interface{}{"feature1"},
					},
				},
			},
			location:    "options.features.enabled",
			value:       "feature2",
			expectError: false,
			expectedVals: map[string]interface{}{
				"options": map[string]interface{}{
					"features": map[string]interface{}{
						"enabled": []interface{}{"feature1", "feature2"},
					},
				},
			},
		},
		{
			name: "add duplicate value - idempotent",
			initialVals: map[string]interface{}{
				"options": map[string]interface{}{
					"features": map[string]interface{}{
						"enabled": []interface{}{"feature1"},
					},
				},
			},
			location:    "options.features.enabled",
			value:       "feature1",
			expectError: false,
			expectedVals: map[string]interface{}{
				"options": map[string]interface{}{
					"features": map[string]interface{}{
						"enabled": []interface{}{"feature1"},
					},
				},
			},
		},
		{
			name: "add with sorting",
			initialVals: map[string]interface{}{
				"options": map[string]interface{}{
					"features": map[string]interface{}{
						"enabled": []interface{}{"feature2", "feature1"},
					},
				},
			},
			location:    "options.features.enabled",
			value:       "feature0",
			expectError: false,
			expectedVals: map[string]interface{}{
				"options": map[string]interface{}{
					"features": map[string]interface{}{
						"enabled": []interface{}{"feature0", "feature1", "feature2"},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hv := NewHelmValues()
			hv.values = tt.initialVals

			err := hv.AddListValue(tt.location, tt.value)

			if tt.expectError && err == nil {
				t.Errorf("Expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if !tt.expectError && !reflect.DeepEqual(hv.values, tt.expectedVals) {
				t.Errorf("Expected %v, got %v", tt.expectedVals, hv.values)
			}
		})
	}
}

func TestRemoveListValue(t *testing.T) {
	tests := []struct {
		name         string
		initialVals  map[string]interface{}
		location     string
		value        string
		expectError  bool
		expectedVals map[string]interface{}
	}{
		{
			name:        "empty location",
			location:    "",
			value:       "test",
			expectError: true,
		},
		{
			name:         "remove from non-existent location - idempotent",
			initialVals:  make(map[string]interface{}),
			location:     "options.features.enabled",
			value:        "feature1",
			expectError:  false,
			expectedVals: map[string]interface{}{},
		},
		{
			name: "remove existing value",
			initialVals: map[string]interface{}{
				"options": map[string]interface{}{
					"features": map[string]interface{}{
						"enabled": []interface{}{"feature1", "feature2"},
					},
				},
			},
			location:    "options.features.enabled",
			value:       "feature1",
			expectError: false,
			expectedVals: map[string]interface{}{
				"options": map[string]interface{}{
					"features": map[string]interface{}{
						"enabled": []interface{}{"feature2"},
					},
				},
			},
		},
		{
			name: "remove last value from list",
			initialVals: map[string]interface{}{
				"options": map[string]interface{}{
					"features": map[string]interface{}{
						"enabled": []interface{}{"feature1"},
					},
				},
			},
			location:    "options.features.enabled",
			value:       "feature1",
			expectError: false,
			expectedVals: map[string]interface{}{
				"options": map[string]interface{}{
					"features": map[string]interface{}{
						"enabled": []interface{}{},
					},
				},
			},
		},
		{
			name: "remove non-existent value - idempotent",
			initialVals: map[string]interface{}{
				"options": map[string]interface{}{
					"features": map[string]interface{}{
						"enabled": []interface{}{"feature1", "feature2"},
					},
				},
			},
			location:    "options.features.enabled",
			value:       "feature3",
			expectError: false,
			expectedVals: map[string]interface{}{
				"options": map[string]interface{}{
					"features": map[string]interface{}{
						"enabled": []interface{}{"feature1", "feature2"},
					},
				},
			},
		},
		{
			name: "remove middle value from list",
			initialVals: map[string]interface{}{
				"options": map[string]interface{}{
					"features": map[string]interface{}{
						"enabled": []interface{}{"feature1", "feature2", "feature3"},
					},
				},
			},
			location:    "options.features.enabled",
			value:       "feature2",
			expectError: false,
			expectedVals: map[string]interface{}{
				"options": map[string]interface{}{
					"features": map[string]interface{}{
						"enabled": []interface{}{"feature1", "feature3"},
					},
				},
			},
		},
		{
			name: "remove from catalogd features",
			initialVals: map[string]interface{}{
				"options": map[string]interface{}{
					"catalogd": map[string]interface{}{
						"features": map[string]interface{}{
							"disabled": []interface{}{"APIV1MetasHandler", "OtherFeature"},
						},
					},
				},
			},
			location:    "options.catalogd.features.disabled",
			value:       "APIV1MetasHandler",
			expectError: false,
			expectedVals: map[string]interface{}{
				"options": map[string]interface{}{
					"catalogd": map[string]interface{}{
						"features": map[string]interface{}{
							"disabled": []interface{}{"OtherFeature"},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hv := NewHelmValues()
			hv.values = tt.initialVals

			err := hv.RemoveListValue(tt.location, tt.value)

			if tt.expectError && err == nil {
				t.Errorf("Expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if !tt.expectError && !reflect.DeepEqual(hv.values, tt.expectedVals) {
				t.Errorf("Expected %v, got %v", tt.expectedVals, hv.values)
			}
		})
	}
}

func TestAddValues(t *testing.T) {
	tests := []struct {
		name         string
		initialVals  map[string]interface{}
		newVals      map[string]interface{}
		expectedVals map[string]interface{}
	}{
		{
			name:        "add to empty values",
			initialVals: make(map[string]interface{}),
			newVals: map[string]interface{}{
				"key": "value",
			},
			expectedVals: map[string]interface{}{
				"key": "value",
			},
		},
		{
			name: "merge nested values",
			initialVals: map[string]interface{}{
				"options": map[string]interface{}{
					"operatorController": map[string]interface{}{
						"features": map[string]interface{}{
							"enabled": []interface{}{"feature1"},
						},
					},
				},
			},
			newVals: map[string]interface{}{
				"options": map[string]interface{}{
					"operatorController": map[string]interface{}{
						"features": map[string]interface{}{
							"disabled": []interface{}{"feature2"},
						},
					},
				},
			},
			expectedVals: map[string]interface{}{
				"options": map[string]interface{}{
					"operatorController": map[string]interface{}{
						"features": map[string]interface{}{
							"enabled":  []interface{}{"feature1"},
							"disabled": []interface{}{"feature2"},
						},
					},
				},
			},
		},
		{
			name: "overwrite values",
			initialVals: map[string]interface{}{
				"key": "oldvalue",
			},
			newVals: map[string]interface{}{
				"key": "newvalue",
			},
			expectedVals: map[string]interface{}{
				"key": "newvalue",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hv := NewHelmValues()
			hv.values = tt.initialVals
			newHv := NewHelmValues()
			newHv.values = tt.newVals

			err := hv.AddValues(newHv)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !reflect.DeepEqual(hv.values, tt.expectedVals) {
				t.Errorf("Expected %v, got %v", tt.expectedVals, hv.values)
			}
		})
	}
}
