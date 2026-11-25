package controller

import (
	"fmt"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// ManagedByLabel identifies objects reconciled by cluster-olm-operator. It is
// deliberately independent of Helm metadata and diagnostic relatedObjects.
const ManagedByLabel = "olm.openshift.io/managed-by"
const ManagedByValue = "cluster-olm-operator"

func markManagedResource(obj *unstructured.Unstructured) ([]byte, error) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[ManagedByLabel] = ManagedByValue
	obj.SetLabels(labels)
	data, err := yaml.Marshal(obj.Object)
	if err != nil {
		return nil, fmt.Errorf("marking managed resource: %w", err)
	}
	return data, nil
}
