package controller

import (
	"context"
	"errors"
	"testing"

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
			name:        "managed, applyFunc returns error, error expected",
			shouldError: true,
			ctrl: &clusterCatalogController{
				name:        "foo",
				key:         types.NamespacedName{Name: "foo"},
				managedFunc: func() (bool, error) { return true, nil },
				applyFunc: func(_ context.Context, _ types.NamespacedName, _ string, _ bool, _ schema.GroupVersionResource, _ []byte) error {
					return errors.New("boom")
				},
			},
		},
		{
			name:        "managed, applyFunc does not return an error, no error expected",
			shouldError: false,
			ctrl: &clusterCatalogController{
				name:        "foo",
				key:         types.NamespacedName{Name: "foo"},
				managedFunc: func() (bool, error) { return true, nil },
				applyFunc: func(_ context.Context, _ types.NamespacedName, _ string, _ bool, _ schema.GroupVersionResource, _ []byte) error {
					return nil
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
