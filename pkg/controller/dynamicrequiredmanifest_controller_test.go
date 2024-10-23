package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	catalogdv1alpha1 "github.com/operator-framework/catalogd/api/core/v1alpha1"
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

func TestClusterCatalogControllerSync(t *testing.T) {
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
			name:        "managed, catalog fetched successfully, should update, applyFunc returns error, error expected",
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
				catalogGetFunc: func(_ types.NamespacedName) (runtime.Object, error) {
					return &unstructured.Unstructured{}, nil
				},
			},
		},
		{
			name:        "managed, catalog successfully fetched, should update, applyFunc does not return an error, no error expected",
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
				catalogGetFunc: func(_ types.NamespacedName) (runtime.Object, error) {
					return &unstructured.Unstructured{}, nil
				},
			},
		},
		{
			name:        "managed, catalog fetch error, error expected",
			assertError: containsError(errors.New("boom")),
			ctrl: &dynamicRequiredManifestController{
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
			assertError: containsError(errors.New("boom")),
			ctrl: &dynamicRequiredManifestController{
				name:        "foo",
				key:         types.NamespacedName{Name: "foo"},
				managedFunc: func() (bool, error) { return true, nil },
				catalogGetFunc: func(_ types.NamespacedName) (runtime.Object, error) {
					return &unstructured.Unstructured{}, nil
				},
				shouldUpdateFunc: func(_ []byte, _ runtime.Object) (bool, error) {
					return false, errors.New("boom")
				},
			},
		},
		{
			name:        "managed, catalog successfully fetched, shouldn't update, applyFunc errors, no error expected",
			assertError: noError(),
			ctrl: &dynamicRequiredManifestController{
				name:        "foo",
				key:         types.NamespacedName{Name: "foo"},
				managedFunc: func() (bool, error) { return true, nil },
				catalogGetFunc: func(_ types.NamespacedName) (runtime.Object, error) {
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
			name:        "managed, catalog not found, should update, applyFunc does not error, no error expected",
			assertError: noError(),
			ctrl: &dynamicRequiredManifestController{
				name:        "foo",
				key:         types.NamespacedName{Name: "foo"},
				managedFunc: func() (bool, error) { return true, nil },
				catalogGetFunc: func(_ types.NamespacedName) (runtime.Object, error) {
					return nil, apierrors.NewNotFound(catalogdv1alpha1.GroupVersion.WithResource("clustercatalogs").GroupResource(), "foo")
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
