package controller

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/openshift/cluster-olm-operator/pkg/helmvalues"

	yaml3 "gopkg.in/yaml.v3"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"

	"k8s.io/klog/v2"
)

// Expected path structure:
// ${assets}/helm/${subDir}/olmv1/ = chart
// ${assets}/helm/${subDir}/openshift.yaml = primary values file
// ${assets}/helm/${subDir}/experimental.yaml = experimental values file
// ${assets}/${subDir}/ = output directory
func (b *Builder) renderHelmTemplate(helmPath, manifestDir string) error {
	log := klog.FromContext(context.Background()).WithName("renderHelmTemplate")
	log.Info("Rendering Helm template", "source", helmPath, "destination", manifestDir)

	useExperimental := b.UseExperimentalFeatureSet()
	clusterGatesConfig, err := b.CurrentFeatureGates()
	if err != nil {
		return fmt.Errorf("CurrentFeatureGates failed: %w", err)
	}

	featureGateValues, err := upstreamFeatureGates(clusterGatesConfig,
		b.Clients.FeatureGateMapper.DownstreamFeatureGates(),
		b.Clients.FeatureGateMapper.UpstreamForDownstream)
	if err != nil {
		return err
	}
	hasEnabledFeatureGates, err := featureGateValues.HasEnabledFeatureGates()
	if err != nil {
		return err
	}
	if hasEnabledFeatureGates {
		useExperimental = true
	}

	// Determine and generate the values from the files (equivalent to --values)
	valuesFiles := []string{filepath.Join(helmPath, "openshift.yaml")}
	if useExperimental {
		log.Info("Using experimental values")
		valuesFiles = append(valuesFiles, filepath.Join(helmPath, "experimental.yaml"))
	} else {
		log.Info("Using standard values")
	}
	values, err := helmvalues.NewHelmValuesFromFiles(valuesFiles)
	if err != nil {
		return fmt.Errorf("error from GatherHelmValuesFromFiles: %w", err)
	}

	// Clear any feature gate settings from helm values files before adding cluster-driven feature gates
	// This ensures cluster feature gate configuration takes precedence over helm defaults
	if err := values.ClearFeatureGates(); err != nil {
		return fmt.Errorf("error from ClearFeatureGates: %w", err)
	}

	// Add the featureGateValues
	if err := values.AddValues(featureGateValues); err != nil {
		return fmt.Errorf("error from AddValues: %w", err)
	}
	// Log verbosity and proxies are dynamic, so they are not included here.
	// Feature flags are listed here, and if they change cluster-olm-operator
	// will resart, as the manifest needs to be regenerated
	// Add more values (equivalent to --set)
	if err := values.SetStringValue("options.catalogd.deployment.image", os.Getenv("CATALOGD_IMAGE")); err != nil {
		return fmt.Errorf("error setting CATALOGD_IMAGE: %w", err)
	}
	if err := values.SetStringValue("options.operatorController.deployment.image", os.Getenv("OPERATOR_CONTROLLER_IMAGE")); err != nil {
		return fmt.Errorf("error setting OPERATOR_CONTROLLER_IMAGE: %w", err)
	}

	log.Info("Calculated values", "values", values.GetValues())

	// Load the helm chart
	chart, err := loader.Load(filepath.Join(helmPath, "olmv1"))
	if err != nil {
		return fmt.Errorf("helm chart Load failed: %w", err)
	}

	// Configure the client
	client := action.NewInstall(&action.Configuration{})
	client.ClientOnly = true
	client.DryRun = true
	client.ReleaseName = "olmv1"
	client.DisableHooks = true

	// Render the chart into memory
	rel, err := client.Run(chart, values.GetValues())
	if err != nil {
		return fmt.Errorf("render Run failed: %w", err)
	}

	// Remove any existing output directory and recreate it
	_ = os.RemoveAll(manifestDir)
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		return fmt.Errorf("MkDirAll %q failed: %w", manifestDir, err)
	}

	// Write the rendered chart into individual files

	// Use a decoder to properly parse multiple YAML documents
	decoder := yaml3.NewDecoder(strings.NewReader(rel.Manifest))

	// Also split by explicit document separators for individual file writing
	documentTexts := splitYAMLDocuments(rel.Manifest)
	log.Info("Found YAML resources", "count", len(documentTexts))

	var documents []DocumentInfo
	docIndex := 0

	for {
		var resource K8sResource
		err := decoder.Decode(&resource)
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return fmt.Errorf("unable to decode YAML resource number %d: %w", docIndex+1, err)
		}

		if resource.Kind == "" {
			return fmt.Errorf("missing Kind field in YAML resource number %d: %w", docIndex+1, err)
		}

		// Find the corresponding text document
		var docText string
		if docIndex < len(documentTexts) {
			docText = documentTexts[docIndex]
		} else {
			// Fallback to marshaling the resource back to YAML
			yamlBytes, err := yaml3.Marshal(&resource)
			if err != nil {
				return fmt.Errorf("error marshaling YAML resource %d: %w", docIndex+1, err)
			}
			docText = string(yamlBytes)
		}

		documents = append(documents, DocumentInfo{
			Resource: resource,
			Text:     docText,
			Order:    docIndex,
		})

		docIndex++
	}

	// Write files in order with numbered prefixes
	validDocs := 0
	for _, doc := range documents {
		// Generate filename with order prefix
		baseFilename := generateFilename(doc.Resource)
		filename := fmt.Sprintf("%02d-%s", doc.Order+1, baseFilename)
		filePath := filepath.Join(manifestDir, filename)

		// Write the document to a separate file
		if err := writeDocument(filePath, doc.Text); err != nil {
			return fmt.Errorf("error writing file=%s: %w", filePath, err)
		}

		log.Info("Created manifest file", "file", filePath, "kind", doc.Resource.Kind, "name", doc.Resource.Metadata.Name)
		validDocs++
	}

	log.Info("Successfully split manifest", "count", validDocs, "directory", manifestDir)
	return nil
}

// YAML Splitting Code

type K8sResource struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace,omitempty"`
	} `yaml:"metadata"`
}

type DocumentInfo struct {
	Resource K8sResource
	Text     string
	Order    int
}

func splitYAMLDocuments(content string) []string {
	// Split by document separators but preserve the original text
	lines := strings.Split(content, "\n")
	var documents []string
	var currentDoc strings.Builder

	for i, line := range lines {
		if strings.TrimSpace(line) == "---" && i > 0 {
			// Start a new document
			if currentDoc.Len() > 0 {
				documents = append(documents, strings.TrimSpace(currentDoc.String()))
				currentDoc.Reset()
			}
		} else {
			if currentDoc.Len() > 0 {
				currentDoc.WriteString("\n")
			}
			currentDoc.WriteString(line)
		}
	}

	// Add the last document
	if currentDoc.Len() > 0 {
		documents = append(documents, strings.TrimSpace(currentDoc.String()))
	}

	// Filter out empty documents and comment-only documents
	var filteredDocs []string
	for _, doc := range documents {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}

		// Check if document has actual content (not just comments)
		lines := strings.Split(doc, "\n")
		hasContent := false
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				hasContent = true
				break
			}
		}
		if hasContent {
			filteredDocs = append(filteredDocs, doc)
		}
	}

	return filteredDocs
}

func generateFilename(resource K8sResource) string {
	// Clean the name to be filesystem-safe
	safeName := sanitizeFilename(resource.Metadata.Name)
	safeKind := sanitizeFilename(strings.ToLower(resource.Kind))

	// Include namespace if present
	if resource.Metadata.Namespace != "" {
		safeNamespace := sanitizeFilename(resource.Metadata.Namespace)
		return fmt.Sprintf("%s-%s-%s.yaml", safeNamespace, safeKind, safeName)
	}

	return fmt.Sprintf("%s-%s.yaml", safeKind, safeName)
}

func sanitizeFilename(name string) string {
	// Replace invalid characters with hyphens
	reg := regexp.MustCompile(`[^a-zA-Z0-9.-]`)
	return reg.ReplaceAllString(name, "-")
}

func writeDocument(filePath, content string) error {
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	// Add the YAML document separator at the beginning if not present
	if !strings.HasPrefix(content, "---") {
		if _, err := writer.WriteString("---\n"); err != nil {
			return err
		}
	}

	if _, err = writer.WriteString(content); err != nil {
		return err
	}
	if !strings.HasSuffix(content, "\n") {
		_, err = writer.WriteString("\n")
	}
	return err
}
