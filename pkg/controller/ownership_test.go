package controller

import (
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
	"testing"
)

func TestMarkManagedResource(t *testing.T) {
	for _, kind := range []string{"ClusterCatalog", "Deployment", "ClusterRole"} {
		t.Run(kind, func(t *testing.T) {
			obj := &unstructured.Unstructured{Object: map[string]interface{}{
				"apiVersion": "example.io/v1", "kind": kind,
				"metadata": map[string]interface{}{"name": "example", "labels": map[string]interface{}{"existing": "keep"}},
				"spec":     map[string]interface{}{"template": map[string]interface{}{"metadata": map[string]interface{}{"labels": map[string]interface{}{"pod": "unchanged"}}}},
			}}
			before := obj.DeepCopy()
			data, err := markManagedResource(obj)
			require.NoError(t, err)
			var decoded map[string]interface{}
			require.NoError(t, yaml.Unmarshal(data, &decoded))
			require.Equal(t, ManagedByValue, obj.GetLabels()[ManagedByLabel])
			require.Equal(t, "keep", obj.GetLabels()["existing"])
			require.Equal(t, before.Object["spec"], decoded["spec"])
			again, err := markManagedResource(obj)
			require.NoError(t, err)
			require.Equal(t, data, again)
		})
	}
}

func TestMarkUnlabelledResource(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": map[string]interface{}{"name": "example"}}}
	_, err := markManagedResource(obj)
	require.NoError(t, err)
	require.Equal(t, map[string]string{ManagedByLabel: ManagedByValue}, obj.GetLabels())
}
