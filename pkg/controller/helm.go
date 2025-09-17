package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/TwiN/deepmerge"
	configv1 "github.com/openshift/api/config/v1"

	yaml3 "gopkg.in/yaml.v3"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/klog/v2"
)

// Expected path structure:
// ${assets}/helm/${subDir}/olmv1/ = chart
// ${assets}/helm/${subDir}/openshift.yaml = primary values file
// ${assets}/helm/${subDir}/experimental.yaml = experimental values file
// ${assets}/${subDir}/ = output directory
func (b *Builder) renderHelmTemplate(subDir string) error {
	helmPath := filepath.Join(b.Assets, "helm", subDir)

	useExperimental := useExperimentalFeatureSet(b.FeatureSet)
	// What featureGates are enabled?
	clusterGatesConfig, err := b.Clients.FeatureGatesAccessor.CurrentFeatureGates()
	if err != nil {
		return err
	}
	catalogdFeatures := upstreamFeatureGates(clusterGatesConfig,
		b.Clients.FeatureGateMapper.CatalogdDownstreamFeatureGates(),
		b.Clients.FeatureGateMapper.CatalogdUpstreamForDownstream)
	operatorControllerFeatures := upstreamFeatureGates(clusterGatesConfig,
		b.Clients.FeatureGateMapper.OperatorControllerDownstreamFeatureGates(),
		b.Clients.FeatureGateMapper.OperatorControllerUpstreamForDownstream)
	if len(catalogdFeatures) > 0 || len(operatorControllerFeatures) > 0 {
		useExperimental = true
	}

	// Determine and generate the values
	valuesFiles := []string{filepath.Join(helmPath, "openshift.yaml")} // possibly -> openshift.yaml
	if useExperimental {
		valuesFiles = append(valuesFiles, filepath.Join(helmPath, "experimental.yaml"))
	}
	values, err := gatherHelmValues(valuesFiles)
	if err != nil {
		return err
	}

	// Log verbosity and proxies are dynamic, so they are not included here.
	// Feature flags are listed here, and if they change cluster-olm-operator
	// will resart, as the manifest needs to be regenerated
	newvalues := []struct {
		location string
		value    any
	}{
		{"catalogdFeatures", catalogdFeatures},
		{"operatorControllerFeatures", operatorControllerFeatures},
		{"options.operatorController.deployment.image", os.Getenv("OPERATOR_CONTROLLER_IMAGE")},
		{"options.catalogd.deployment.image", os.Getenv("CATALOGD_IMAGE")},
	}

	for _, v := range newvalues {
		values, err = setHelmValue(values, v.location, v.value)
		if err != nil {
			return err
		}
	}

	// Load the helm chart
	chart, err := loader.Load(filepath.Join(helmPath, "olmv1"))
	if err != nil {
		return err
	}

	// Configure the client
	client := action.NewInstall(&action.Configuration{})
	client.ClientOnly = true
	client.DryRun = true
	client.ReleaseName = "olmv1"
	client.DisableHooks = true

	// Render the chart into memory
	rel, err := client.Run(chart, values)
	if err != nil {
		return err
	}

	// Remove any existing output directory and recreate it
	manifestDir := filepath.Join(b.Assets, subDir)
	_ = os.RemoveAll(manifestDir)
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		return err
	}

	// Write the rendered chart into individual files
	d := yaml3.NewDecoder(strings.NewReader(rel.Manifest))
	for i := 0; ; i++ {
		var us unstructured.Unstructured
		err = d.Decode(&us)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}

		var fileName string
		if us.GetNamespace() != "" {
			fileName = fmt.Sprintf("%02.2d-%s-%s-%s.yaml", i, us.GetKind(), us.GetNamespace(), us.GetName())
		} else {
			fileName = fmt.Sprintf("%02.2d-%s-%s.yaml", i, us.GetKind(), us.GetName())
		}
		fileName = filepath.Join(manifestDir, fileName)
		data, err := yaml3.Marshal(us)
		if err != nil {
			return err
		}
		if err := os.WriteFile(fileName, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func useExperimentalFeatureSet(fs configv1.FeatureSet) bool {
	switch fs {
	case configv1.CustomNoUpgrade:
		return true
	case configv1.DevPreviewNoUpgrade:
		return true
	case configv1.TechPreviewNoUpgrade:
		return true
	case configv1.Default:
	default:
		klog.FromContext(context.Background()).WithName("builder").V(4).Info("Unknown featureSet value, using standard manifests", "featureSet", fs)
	}
	return false
}

func gatherHelmValues(files []string) (map[string]any, error) {
	values := make(map[string]any)
	for _, a := range files {
		newvalues := make(map[string]any)
		data, err := os.ReadFile(a)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(data, newvalues); err != nil {
			return nil, err
		}
		if err := deepmerge.DeepMerge(values, newvalues, deepmerge.Config{}); err != nil {
			return nil, err
		}
	}
	return values, nil
}

func setHelmValue(values map[string]any, location string, value any) (map[string]any, error) {
	ss := strings.Split(location, ".")
	if len(ss) < 1 {
		return nil, errors.New("location string has no locations")
	}

	// Build a tree in reverse
	slices.Reverse(ss)
	v := make(map[string]any)
	v[ss[0]] = value
	for _, s := range ss[1:] {
		newmap := make(map[string]any)
		newmap[s] = v
		v = newmap
	}

	if err := deepmerge.DeepMerge(values, v, deepmerge.Config{}); err != nil {
		return nil, err
	}
	return values, nil
}
