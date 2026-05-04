package controller

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
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

	// Get current feature gates once and reuse
	clusterGatesConfig, err := b.CurrentFeatureGates()
	if err != nil {
		return fmt.Errorf("CurrentFeatureGates failed: %w", err)
	}

	// Check if any feature gates are enabled (without calling upstreamFeatureGates)
	hasEnabledFeatureGates, err := b.HasEnabledDownstreamFeatureGates(clusterGatesConfig)
	if err != nil {
		return fmt.Errorf("HasEnabledDownstreamFeatureGates failed: %w", err)
	}

	// Determine if we should use experimental values file based on FeatureSet
	// OR enabled feature gates. This uses the same logic as the RBAC wait check
	// in main.go to ensure consistency.
	useExperimental := b.UseExperimentalFeatureSet() || hasEnabledFeatureGates

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

	// Add the upstreamFeatureGates to populate the actual helm values with enabled/disabled features.
	// This is called only once here to merge feature gates into the values loaded from files.
	values, err = upstreamFeatureGates(
		values,
		clusterGatesConfig,
		b.Clients.FeatureGateMapper.DownstreamFeatureGates(),
		b.Clients.FeatureGateMapper.UpstreamForDownstream)
	if err != nil {
		return err
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

	// On HighlyAvailable topologies scale to 2 replicas and enable the PDB so that rolling
	// updates never leave zero running pods. On SingleReplica (SNO) / External topologies
	// the manifest defaults (replicas=1, PDB disabled) are kept as-is.
	if b.Infrastructure != nil && isHighlyAvailableTopology(b.Infrastructure) {
		log.Info("HighlyAvailable topology detected, setting replicas=2 and enabling PDB")
		haOverrides := []struct {
			key   string
			value interface{}
		}{
			{"options.catalogd.deployment.replicas", 2},
			{"options.operatorController.deployment.replicas", 2},
			{"options.catalogd.podDisruptionBudget.enabled", true},
			{"options.operatorController.podDisruptionBudget.enabled", true},
		}
		for _, o := range haOverrides {
			var err error
			switch v := o.value.(type) {
			case int:
				err = values.SetIntValue(o.key, v)
			case bool:
				err = values.SetBoolValue(o.key, v)
			}
			if err != nil {
				return fmt.Errorf("error setting %s: %w", o.key, err)
			}
		}
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

func isHighlyAvailableTopology(infra *configv1.Infrastructure) bool {
	switch infra.Status.ControlPlaneTopology {
	case configv1.HighlyAvailableTopologyMode,
		configv1.HighlyAvailableArbiterMode,
		configv1.DualReplicaTopologyMode:
		return true
	}
	return false
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
