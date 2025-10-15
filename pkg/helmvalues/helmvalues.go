package helmvalues

import (
	"errors"
	"fmt"
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
			return fmt.Errorf("newValue=%q already present in values=%v", newValue, values)
		}
		values = append(values, newValue)
		slices.Sort(values)
		return unstructured.SetNestedStringSlice(v.values, values, ss...)
	}
	return unstructured.SetNestedStringSlice(v.values, []string{newValue}, ss...)
}

func (v *HelmValues) AddValues(newValues *HelmValues) error {
	return deepmerge.DeepMerge(v.values, newValues.values, deepmerge.Config{})
}
