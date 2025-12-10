package controller

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/stretchr/testify/require"
	yaml3 "gopkg.in/yaml.v3"

	internalfeatures "github.com/openshift/cluster-olm-operator/internal/featuregates"
	"github.com/openshift/cluster-olm-operator/pkg/clients"
)

func TestRenderHelmTemplate(t *testing.T) {
	// We need to copy the files into a temp directory
	testDir, err := os.MkdirTemp("", "helm-*")
	require.NoError(t, err)
	defer os.RemoveAll(testDir)

	// Get back to repo root directory
	err = os.Chdir("../..")
	require.NoError(t, err)

	cwd, err := os.Getwd()
	require.NoError(t, err)

	b := Builder{
		Assets: filepath.Join(cwd, "testdata"),
		Clients: &clients.Clients{
			FeatureGateMapper: internalfeatures.NewMapper(),
		},
		FeatureGate: configv1.FeatureGate{
			Status: configv1.FeatureGateStatus{
				FeatureGates: []configv1.FeatureGateDetails{},
			},
		},
	}

	require.NoError(t, b.renderHelmTemplate(b.Assets, testDir))

	compareDir := os.Getenv("HELM_OUTPUT")
	require.NotEmpty(t, compareDir)
	os.Remove(compareDir)

	compareFile := filepath.Join(compareDir, "hello-world.yaml")
	compareData, err := os.ReadFile(compareFile)
	require.NoError(t, err)

	var testData []byte

	err = filepath.WalkDir(testDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml" {
			return nil
		}
		t.Logf("Reading file %q", path)
		tmpData, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		testData = append(testData, tmpData...)

		return nil
	})
	require.NoError(t, err)
	require.Equal(t, compareData, testData)
}

func TestSplitYAMLDocuments(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "empty input",
			input:    "",
			expected: nil,
		},
		{
			name:     "single document",
			input:    "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test",
			expected: []string{"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test"},
		},
		{
			name: "multiple documents with separators",
			input: `apiVersion: v1
kind: ConfigMap
metadata:
  name: test1
---
apiVersion: v1
kind: Secret
metadata:
  name: test2`,
			expected: []string{
				"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test1",
				"apiVersion: v1\nkind: Secret\nmetadata:\n  name: test2",
			},
		},
		{
			name: "documents with empty sections",
			input: `apiVersion: v1
kind: ConfigMap
metadata:
  name: test1
---

---
apiVersion: v1
kind: Secret
metadata:
  name: test2`,
			expected: []string{
				"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test1",
				"apiVersion: v1\nkind: Secret\nmetadata:\n  name: test2",
			},
		},
		{
			name: "documents with comments only",
			input: `apiVersion: v1
kind: ConfigMap
metadata:
  name: test1
---
# This is just a comment
# Another comment
---
apiVersion: v1
kind: Secret
metadata:
  name: test2`,
			expected: []string{
				"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test1",
				"apiVersion: v1\nkind: Secret\nmetadata:\n  name: test2",
			},
		},
		{
			name: "documents with mixed content",
			input: `# Header comment
apiVersion: v1
kind: ConfigMap
metadata:
  name: test1
  # inline comment
data:
  key: value
---
apiVersion: v1
kind: Secret
metadata:
  name: test2
type: Opaque`,
			expected: []string{
				"# Header comment\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test1\n  # inline comment\ndata:\n  key: value",
				"apiVersion: v1\nkind: Secret\nmetadata:\n  name: test2\ntype: Opaque",
			},
		},
		{
			name: "documents with leading separator",
			input: `---
apiVersion: v1
kind: ConfigMap
metadata:
  name: test1
---
apiVersion: v1
kind: Secret
metadata:
  name: test2`,
			expected: []string{
				"---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test1",
				"apiVersion: v1\nkind: Secret\nmetadata:\n  name: test2",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitYAMLDocuments(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("splitYAMLDocuments() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGenerateFilename(t *testing.T) {
	tests := []struct {
		name     string
		resource K8sResource
		expected string
	}{
		{
			name: "basic resource without namespace",
			resource: K8sResource{
				Kind: "ConfigMap",
				Metadata: struct {
					Name      string `yaml:"name"`
					Namespace string `yaml:"namespace,omitempty"`
				}{
					Name: "my-config",
				},
			},
			expected: "configmap-my-config.yaml",
		},
		{
			name: "resource with namespace",
			resource: K8sResource{
				Kind: "Secret",
				Metadata: struct {
					Name      string `yaml:"name"`
					Namespace string `yaml:"namespace,omitempty"`
				}{
					Name:      "my-secret",
					Namespace: "kube-system",
				},
			},
			expected: "kube-system-secret-my-secret.yaml",
		},
		{
			name: "resource with special characters",
			resource: K8sResource{
				Kind: "ServiceAccount",
				Metadata: struct {
					Name      string `yaml:"name"`
					Namespace string `yaml:"namespace,omitempty"`
				}{
					Name:      "test@user_account",
					Namespace: "my-namespace!",
				},
			},
			expected: "my-namespace--serviceaccount-test-user-account.yaml",
		},
		{
			name: "cluster-scoped resource",
			resource: K8sResource{
				Kind: "ClusterRole",
				Metadata: struct {
					Name      string `yaml:"name"`
					Namespace string `yaml:"namespace,omitempty"`
				}{
					Name: "cluster-admin",
				},
			},
			expected: "clusterrole-cluster-admin.yaml",
		},
		{
			name: "custom resource",
			resource: K8sResource{
				Kind: "MyCustomResource",
				Metadata: struct {
					Name      string `yaml:"name"`
					Namespace string `yaml:"namespace,omitempty"`
				}{
					Name:      "custom-instance",
					Namespace: "default",
				},
			},
			expected: "default-mycustomresource-custom-instance.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateFilename(tt.resource)
			if result != tt.expected {
				t.Errorf("generateFilename() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "valid filename unchanged",
			input:    "valid-filename.yaml",
			expected: "valid-filename.yaml",
		},
		{
			name:     "replace special characters",
			input:    "my@file$name&test",
			expected: "my-file-name-test",
		},
		{
			name:     "replace spaces",
			input:    "file name with spaces",
			expected: "file-name-with-spaces",
		},
		{
			name:     "replace slashes",
			input:    "path/to/file",
			expected: "path-to-file",
		},
		{
			name:     "preserve valid characters",
			input:    "file123-name.test",
			expected: "file123-name.test",
		},
		{
			name:     "unicode characters",
			input:    "файл名前",
			expected: "------",
		},
		{
			name:     "mixed valid and invalid",
			input:    "test_file@2023.yaml",
			expected: "test-file-2023.yaml",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "only special characters",
			input:    "@#$%^&*()",
			expected: "---------",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeFilename(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeFilename() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestWriteDocument(t *testing.T) {
	tests := []struct {
		name           string
		content        string
		expectPrefix   bool
		expectNewline  bool
		expectedOutput string
	}{
		{
			name:           "content without document separator",
			content:        "apiVersion: v1\nkind: ConfigMap",
			expectPrefix:   true,
			expectNewline:  true,
			expectedOutput: "---\napiVersion: v1\nkind: ConfigMap\n",
		},
		{
			name:           "content with document separator",
			content:        "---\napiVersion: v1\nkind: ConfigMap",
			expectPrefix:   false,
			expectNewline:  true,
			expectedOutput: "---\napiVersion: v1\nkind: ConfigMap\n",
		},
		{
			name:           "content with trailing newline",
			content:        "apiVersion: v1\nkind: ConfigMap\n",
			expectPrefix:   true,
			expectNewline:  false,
			expectedOutput: "---\napiVersion: v1\nkind: ConfigMap\n",
		},
		{
			name:           "empty content",
			content:        "",
			expectPrefix:   true,
			expectNewline:  true,
			expectedOutput: "---\n\n",
		},
		{
			name:           "single line content",
			content:        "test: value",
			expectPrefix:   true,
			expectNewline:  true,
			expectedOutput: "---\ntest: value\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			filePath := filepath.Join(tmpDir, "test.yaml")

			err := writeDocument(filePath, tt.content)
			if err != nil {
				t.Fatalf("writeDocument() error = %v", err)
			}

			// Read the written file
			data, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("Failed to read written file: %v", err)
			}

			result := string(data)
			if result != tt.expectedOutput {
				t.Errorf("writeDocument() output = %q, want %q", result, tt.expectedOutput)
			}

			// Verify document separator behavior
			if tt.expectPrefix && !strings.HasPrefix(result, "---\n") {
				t.Errorf("Expected document separator prefix, but not found")
			}

			// Verify newline behavior
			if tt.expectNewline && !strings.HasSuffix(result, "\n") {
				t.Errorf("Expected trailing newline, but not found")
			}
		})
	}
}

func TestWriteDocument_FileErrors(t *testing.T) {
	t.Run("invalid directory", func(t *testing.T) {
		err := writeDocument("/invalid/directory/file.yaml", "content")
		if err == nil {
			t.Error("Expected error when writing to invalid directory")
		}
	})

	t.Run("read-only directory", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("Skipping test when running as root")
		}

		tmpDir := t.TempDir()

		// Make directory read-only
		err := os.Chmod(tmpDir, 0444)
		if err != nil {
			t.Fatalf("Failed to make directory read-only: %v", err)
		}

		// Restore permissions for cleanup
		defer func() {
			_ = os.Chmod(tmpDir, 0755)
		}()

		filePath := filepath.Join(tmpDir, "test.yaml")
		err = writeDocument(filePath, "content")
		if err == nil {
			t.Error("Expected error when writing to read-only directory")
		}
	})
}

func TestK8sResourceStructure(t *testing.T) {
	t.Run("yaml unmarshaling", func(t *testing.T) {
		yamlContent := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-deployment
  namespace: test-namespace
`
		var resource K8sResource
		err := yaml3.Unmarshal([]byte(yamlContent), &resource)
		if err != nil {
			t.Fatalf("Failed to unmarshal YAML: %v", err)
		}

		if resource.APIVersion != "apps/v1" {
			t.Errorf("Expected APIVersion 'apps/v1', got %q", resource.APIVersion)
		}
		if resource.Kind != "Deployment" {
			t.Errorf("Expected Kind 'Deployment', got %q", resource.Kind)
		}
		if resource.Metadata.Name != "test-deployment" {
			t.Errorf("Expected Name 'test-deployment', got %q", resource.Metadata.Name)
		}
		if resource.Metadata.Namespace != "test-namespace" {
			t.Errorf("Expected Namespace 'test-namespace', got %q", resource.Metadata.Namespace)
		}
	})

	t.Run("yaml marshaling", func(t *testing.T) {
		resource := K8sResource{
			APIVersion: "v1",
			Kind:       "ConfigMap",
			Metadata: struct {
				Name      string `yaml:"name"`
				Namespace string `yaml:"namespace,omitempty"`
			}{
				Name:      "test-config",
				Namespace: "default",
			},
		}

		data, err := yaml3.Marshal(&resource)
		if err != nil {
			t.Fatalf("Failed to marshal YAML: %v", err)
		}

		yamlStr := string(data)
		if !strings.Contains(yamlStr, "apiVersion: v1") {
			t.Error("Marshaled YAML missing apiVersion")
		}
		if !strings.Contains(yamlStr, "kind: ConfigMap") {
			t.Error("Marshaled YAML missing kind")
		}
		if !strings.Contains(yamlStr, "name: test-config") {
			t.Error("Marshaled YAML missing name")
		}
		if !strings.Contains(yamlStr, "namespace: default") {
			t.Error("Marshaled YAML missing namespace")
		}
	})
}

func TestDocumentInfo(t *testing.T) {
	resource := K8sResource{
		Kind: "ConfigMap",
		Metadata: struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace,omitempty"`
		}{
			Name: "test",
		},
	}

	doc := DocumentInfo{
		Resource: resource,
		Text:     "apiVersion: v1\nkind: ConfigMap",
		Order:    5,
	}

	if doc.Resource.Kind != "ConfigMap" {
		t.Errorf("Expected Kind 'ConfigMap', got %q", doc.Resource.Kind)
	}
	if doc.Order != 5 {
		t.Errorf("Expected Order 5, got %d", doc.Order)
	}
	if !strings.Contains(doc.Text, "ConfigMap") {
		t.Error("Expected Text to contain 'ConfigMap'")
	}
}
