package controller

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

func TestClusterCatalogControllerSync(t *testing.T) {
	for _, tc := range []struct {
		name        string
		ctrl        *clusterCatalogController
		shouldError bool
	}{
		{
			name:        "not managed, managedFunc returns error, error expected",
			shouldError: true,
			ctrl: &clusterCatalogController{
				name:        "foo",
				key:         types.NamespacedName{Name: "foo"},
				managedFunc: func() (bool, error) { return false, errors.New("boom") },
			},
		},
		{
			name:        "not managed, managedFunc does not return an error, no error expected",
			shouldError: false,
			ctrl: &clusterCatalogController{
				name:        "foo",
				key:         types.NamespacedName{Name: "foo"},
				managedFunc: func() (bool, error) { return false, nil },
			},
		},
		{
			name:        "managed, catalog fetched successfully, should update, applyFunc returns error, error expected",
			shouldError: true,
			ctrl: &clusterCatalogController{
				name:        "foo",
				key:         types.NamespacedName{Name: "foo"},
				managedFunc: func() (bool, error) { return true, nil },
				applyFunc: func(_ context.Context, _ types.NamespacedName, _ string, _ bool, _ schema.GroupVersionResource, _ []byte) error {
					return errors.New("boom")
				},
				shouldUpdateFunc: func(_ []byte, _ *unstructured.Unstructured) (bool, error) {
					return true, nil
				},
				catalogGetFunc: func(_ types.NamespacedName) (runtime.Object, error) {
					return &unstructured.Unstructured{}, nil
				},
			},
		},
		{
			name:        "managed, catalog successfully fetched, should update, applyFunc does not return an error, no error expected",
			shouldError: false,
			ctrl: &clusterCatalogController{
				name:        "foo",
				key:         types.NamespacedName{Name: "foo"},
				managedFunc: func() (bool, error) { return true, nil },
				applyFunc: func(_ context.Context, _ types.NamespacedName, _ string, _ bool, _ schema.GroupVersionResource, _ []byte) error {
					return nil
				},
				shouldUpdateFunc: func(_ []byte, _ *unstructured.Unstructured) (bool, error) {
					return true, nil
				},
				catalogGetFunc: func(_ types.NamespacedName) (runtime.Object, error) {
					return &unstructured.Unstructured{}, nil
				},
			},
		},
		{
			name:        "managed, catalog fetch error, error expected",
			shouldError: true,
			ctrl: &clusterCatalogController{
				name:        "foo",
				key:         types.NamespacedName{Name: "foo"},
				managedFunc: func() (bool, error) { return true, nil },
				catalogGetFunc: func(_ types.NamespacedName) (runtime.Object, error) {
					return nil, errors.New("boom")
				},
			},
		},
		{
			name:        "managed, catalog successfully fetched, shouldUpdateFunc errors, error expected",
			shouldError: true,
			ctrl: &clusterCatalogController{
				name:        "foo",
				key:         types.NamespacedName{Name: "foo"},
				managedFunc: func() (bool, error) { return true, nil },
				catalogGetFunc: func(_ types.NamespacedName) (runtime.Object, error) {
					return &unstructured.Unstructured{}, nil
				},
				shouldUpdateFunc: func(_ []byte, _ *unstructured.Unstructured) (bool, error) {
					return false, errors.New("boom")
				},
			},
		},
		{
			name:        "managed, catalog successfully fetched, shouldn't update, applyFunc errors, no error expected",
			shouldError: false,
			ctrl: &clusterCatalogController{
				name:        "foo",
				key:         types.NamespacedName{Name: "foo"},
				managedFunc: func() (bool, error) { return true, nil },
				catalogGetFunc: func(_ types.NamespacedName) (runtime.Object, error) {
					return &unstructured.Unstructured{}, nil
				},
				shouldUpdateFunc: func(_ []byte, _ *unstructured.Unstructured) (bool, error) {
					return false, nil
				},
				applyFunc: func(_ context.Context, _ types.NamespacedName, _ string, _ bool, _ schema.GroupVersionResource, _ []byte) error {
					return errors.New("boom")
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.ctrl.sync(context.TODO(), nil)
			if (err != nil) != tc.shouldError {
				t.Errorf("error state does not match expected. shouldError: %v, err: %v", tc.shouldError, err)
			}
		})
	}
}
