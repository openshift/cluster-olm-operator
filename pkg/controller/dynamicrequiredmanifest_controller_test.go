package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	catalogdv1 "github.com/operator-framework/catalogd/api/v1"
)

func containsError(expected error) func(t *testing.T, err error) {
	return func(t *testing.T, err error) {
		if err == nil || !strings.Contains(err.Error(), expected.Error()) {
			t.Fatalf("expected an error with substring %q but received error %v", expected.Error(), err)
		}
	}
}

func noError() func(t *testing.T, err error) {
	return func(t *testing.T, err error) {
		if err != nil {
			t.Fatalf("expected no error but received error %v", err)
		}
	}
}

func TestDynamicRequiredManifestControllerSync(t *testing.T) {
	for _, tc := range []struct {
		name        string
		ctrl        *dynamicRequiredManifestController
		assertError func(t *testing.T, err error)
	}{
		{
			name:        "not managed, managedFunc returns error, error expected",
			assertError: containsError(errors.New("boom")),
			ctrl: &dynamicRequiredManifestController{
				name:        "foo",
				key:         types.NamespacedName{Name: "foo"},
				managedFunc: func() (bool, error) { return false, errors.New("boom") },
			},
		},
		{
			name:        "not managed, managedFunc does not return an error, no error expected",
			assertError: noError(),
			ctrl: &dynamicRequiredManifestController{
				name:        "foo",
				key:         types.NamespacedName{Name: "foo"},
				managedFunc: func() (bool, error) { return false, nil },
			},
		},
		{
			name:        "managed, resource fetched successfully, should update, applyFunc returns error, error expected",
			assertError: containsError(errors.New("boom")),
			ctrl: &dynamicRequiredManifestController{
				name:        "foo",
				key:         types.NamespacedName{Name: "foo"},
				managedFunc: func() (bool, error) { return true, nil },
				applyFunc: func(_ context.Context, _ types.NamespacedName, _ string, _ bool, _ schema.GroupVersionResource, _ []byte) error {
					return errors.New("boom")
				},
				shouldUpdateFunc: func(_ []byte, _ runtime.Object) (bool, error) {
					return true, nil
				},
				objectGetFunc: func(_ types.NamespacedName) (runtime.Object, error) {
					return &unstructured.Unstructured{}, nil
				},
			},
		},
		{
			name:        "managed, resource successfully fetched, should update, applyFunc does not return an error, no error expected",
			assertError: noError(),
			ctrl: &dynamicRequiredManifestController{
				name:        "foo",
				key:         types.NamespacedName{Name: "foo"},
				managedFunc: func() (bool, error) { return true, nil },
				applyFunc: func(_ context.Context, _ types.NamespacedName, _ string, _ bool, _ schema.GroupVersionResource, _ []byte) error {
					return nil
				},
				shouldUpdateFunc: func(_ []byte, _ runtime.Object) (bool, error) {
					return true, nil
				},
				objectGetFunc: func(_ types.NamespacedName) (runtime.Object, error) {
					return &unstructured.Unstructured{}, nil
				},
			},
		},
		{
			name:        "managed, resource fetch error, error expected",
			assertError: containsError(errors.New("boom")),
			ctrl: &dynamicRequiredManifestController{
				name:        "foo",
				key:         types.NamespacedName{Name: "foo"},
				managedFunc: func() (bool, error) { return true, nil },
				objectGetFunc: func(_ types.NamespacedName) (runtime.Object, error) {
					return nil, errors.New("boom")
				},
			},
		},
		{
			name:        "managed, resource successfully fetched, shouldUpdateFunc errors, error expected",
			assertError: containsError(errors.New("boom")),
			ctrl: &dynamicRequiredManifestController{
				name:        "foo",
				key:         types.NamespacedName{Name: "foo"},
				managedFunc: func() (bool, error) { return true, nil },
				objectGetFunc: func(_ types.NamespacedName) (runtime.Object, error) {
					return &unstructured.Unstructured{}, nil
				},
				shouldUpdateFunc: func(_ []byte, _ runtime.Object) (bool, error) {
					return false, errors.New("boom")
				},
			},
		},
		{
			name:        "managed, resource successfully fetched, shouldn't update, applyFunc errors, no error expected",
			assertError: noError(),
			ctrl: &dynamicRequiredManifestController{
				name:        "foo",
				key:         types.NamespacedName{Name: "foo"},
				managedFunc: func() (bool, error) { return true, nil },
				objectGetFunc: func(_ types.NamespacedName) (runtime.Object, error) {
					return &unstructured.Unstructured{}, nil
				},
				shouldUpdateFunc: func(_ []byte, _ runtime.Object) (bool, error) {
					return false, nil
				},
				applyFunc: func(_ context.Context, _ types.NamespacedName, _ string, _ bool, _ schema.GroupVersionResource, _ []byte) error {
					return errors.New("boom")
				},
			},
		},
		{
			name:        "managed, resource not found, should update, applyFunc does not error, no error expected",
			assertError: noError(),
			ctrl: &dynamicRequiredManifestController{
				name:        "foo",
				key:         types.NamespacedName{Name: "foo"},
				managedFunc: func() (bool, error) { return true, nil },
				objectGetFunc: func(_ types.NamespacedName) (runtime.Object, error) {
					return nil, apierrors.NewNotFound(catalogdv1.GroupVersion.WithResource("clusterresources").GroupResource(), "foo")
				},
				shouldUpdateFunc: func(_ []byte, _ runtime.Object) (bool, error) {
					return true, nil
				},
				applyFunc: func(_ context.Context, _ types.NamespacedName, _ string, _ bool, _ schema.GroupVersionResource, _ []byte) error {
					return nil
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.ctrl.sync(context.TODO(), nil)
			tc.assertError(t, err)
		})
	}
}

func TestUnstructuredShouldUpdateFunc(t *testing.T) {
	for _, tc := range []struct {
		name         string
		manifest     []byte
		existing     runtime.Object
		expectUpdate bool
		assertError  func(t *testing.T, err error)
	}{
		{
			name:         "existing is nil, no error, update needed",
			existing:     nil,
			expectUpdate: true,
			assertError:  noError(),
		},
		{
			name:         "existing is not *unstructured.Unstructured, error",
			existing:     &corev1.Pod{},
			expectUpdate: false,
			assertError:  containsError(errors.New("expected existing to be of type *unstructured.Unstructured but was")),
		},
		{
			name:     "required and existing are not deep derivative, no error, update needed",
			manifest: []byte(requiredYAML),
			existing: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "olm.operatorframework.io/v1",
					"kind":       "ClusterCatalog",
					"metadata": map[string]interface{}{
						"name": "openshift-certified-operators",
					},
					"spec": map[string]interface{}{
						"source": map[string]interface{}{
							"type": "Image",
							"image": map[string]interface{}{
								"pollInterval": "1h",
								"ref":          "foobarbaz",
							},
						},
					},
				},
			},
			expectUpdate: true,
			assertError:  noError(),
		},
		{
			name:     "required and existing are deep derivative, no error, no update needed",
			manifest: []byte(requiredYAML),
			existing: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "olm.operatorframework.io/v1",
					"kind":       "ClusterCatalog",
					"metadata": map[string]interface{}{
						"name": "openshift-certified-operators",
						"labels": map[string]string{
							"mycustomlabel": "foobar",
						},
					},
					"spec": map[string]interface{}{
						"source": map[string]interface{}{
							"type": "Image",
							"image": map[string]interface{}{
								"pollInterval": "10m0s",
								"ref":          "registry.redhat.io/redhat/certified-operator-index:v4.18",
							},
						},
					},
				},
			},
			expectUpdate: false,
			assertError:  noError(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updateFunc := unstructuredShouldUpdateFunc()
			needsUpdate, err := updateFunc(tc.manifest, tc.existing)
			tc.assertError(t, err)
			if needsUpdate != tc.expectUpdate {
				t.Fatalf("updateFunc return value doesn't match expected. expected needsUpdate: %v, actual needsUpdate: %v", tc.expectUpdate, needsUpdate)
			}
		})
	}
}

const requiredYAML = `
---
apiVersion: olm.operatorframework.io/v1
kind: ClusterCatalog
metadata:
  name: openshift-certified-operators
spec:
  source:
    type: Image
    image:
      pollInterval: 10m0s
      ref: registry.redhat.io/redhat/certified-operator-index:v4.18
`
