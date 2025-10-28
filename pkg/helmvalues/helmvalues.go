package helmvalues

import (
	"errors"
	"os"
	"slices"
	"strings"

	"github.com/TwiN/deepmerge"

	yaml3 "gopkg.in/yaml.v3"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	// Helm Values Locations
	EnableOperatorController  = "options.operatorController.features.enabled"
	DisableOperatorController = "options.operatorController.features.disabled"
	EnableCatalogd            = "options.catalogd.features.enabled"
	DisableCatalogd           = "options.catalogd.features.disabled"
)

type HelmValues struct {
	values map[string]interface{}
}

func NewHelmValues() *HelmValues {
	return &HelmValues{
		values: make(map[string]interface{}),
	}
}

func NewHelmValuesFromFiles(files []string) (*HelmValues, error) {
	values := NewHelmValues()
	for _, a := range files {
		newvalues := make(map[string]any)
		data, err := os.ReadFile(a)
		if err != nil {
			return nil, err
		}
		if err := yaml3.Unmarshal(data, newvalues); err != nil {
			return nil, err
		}
		if err := deepmerge.DeepMerge(values.values, newvalues, deepmerge.Config{}); err != nil {
			return nil, err
		}
	}
	return values, nil
}

func (v *HelmValues) GetValues() map[string]interface{} {
	return v.values
}

func (v *HelmValues) HasEnabledFeatureGates() (bool, error) {
	ss := strings.Split(EnableOperatorController, ".")
	values, found, err := unstructured.NestedStringSlice(v.values, ss...)
	if err != nil {
		return false, err
	}
	if found && len(values) > 0 {
		return true, nil
	}
	ss = strings.Split(EnableCatalogd, ".")
	values, found, err = unstructured.NestedStringSlice(v.values, ss...)
	if err != nil {
		return false, err
	}
	if found && len(values) > 0 {
		return true, nil
	}
	return false, nil
}

func (v *HelmValues) SetStringValue(location string, newValue string) error {
	if location == "" {
		return errors.New("location string has no locations")
	}
	ss := strings.Split(location, ".")

	return unstructured.SetNestedField(v.values, newValue, ss...)
}

func (v *HelmValues) AddListValue(location string, newValue string) error {
	if location == "" {
		return errors.New("location string has no locations")
	}
	ss := strings.Split(location, ".")
	values, found, err := unstructured.NestedStringSlice(v.values, ss...)
	if err != nil {
		return err
	}
	if found {
		if slices.Index(values, newValue) != -1 {
			// if newValue is already there, then it's been "added"
			return nil
		}
		values = append(values, newValue)
		slices.Sort(values)
		return unstructured.SetNestedStringSlice(v.values, values, ss...)
	}
	return unstructured.SetNestedStringSlice(v.values, []string{newValue}, ss...)
}

func (v *HelmValues) RemoveListValue(location string, rmValue string) error {
	if location == "" {
		return errors.New("location string has no locations")
	}
	ss := strings.Split(location, ".")
	values, found, err := unstructured.NestedStringSlice(v.values, ss...)
	if err != nil {
		return err
	}
	if !found {
		// slice doesn't exist, so rmValue value doesn't exist
		return nil
	}
	idx := slices.Index(values, rmValue)
	if idx == -1 {
		// if rmValue is not already there, then it's been "removed"
		return nil
	}

	values = append(values[:idx], values[idx+1:]...)
	return unstructured.SetNestedStringSlice(v.values, values, ss...)
}

func (v *HelmValues) AddValues(newValues *HelmValues) error {
	return deepmerge.DeepMerge(v.values, newValues.values, deepmerge.Config{})
}

// ClearFeatureGates removes all feature gate settings from the helm values
// This is used to ensure cluster feature gate configuration takes precedence
// over any feature gates defined in helm values files
func (v *HelmValues) ClearFeatureGates() error {
	locationsToClear := []string{
		EnableOperatorController,
		DisableOperatorController,
		EnableCatalogd,
		DisableCatalogd,
	}
	for _, location := range locationsToClear {
		if _, found, _ := unstructured.NestedStringSlice(v.values, strings.Split(location, ".")...); found {
			if err := unstructured.SetNestedStringSlice(v.values, []string{}, strings.Split(location, ".")...); err != nil {
				return err
			}
		}
	}
	return nil
}
