package controller

import (
	"context"
	"errors"
	"reflect"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

type mockCatalogGetter struct {
	obj *unstructured.Unstructured
	err error
}

func (mcg *mockCatalogGetter) Get(_ types.NamespacedName) (runtime.Object, error) {
	return mcg.obj, mcg.err
}

type mockApplier struct {
	err error
}

func (ma *mockApplier) Apply(_ context.Context, _, _ *unstructured.Unstructured) error {
	return ma.err
}

type mockRequirementsEnsurer struct {
	obj      *unstructured.Unstructured
	modified bool
	err      error
}

func (mre *mockRequirementsEnsurer) EnsureRequirements(_ *unstructured.Unstructured) (bool, error) {
	return mre.modified, mre.err
}

func (mre *mockRequirementsEnsurer) Required() *unstructured.Unstructured {
	return mre.obj
}

func TestClusterCatalogControllerSync(t *testing.T) {
	for _, tc := range []struct {
		name        string
		ctrl        *clusterCatalogController
		shouldError bool
	}{
		{
			name:        "not managed, isManagedFunc returns error, error expected",
			shouldError: true,
			ctrl: &clusterCatalogController{
				name:          "foo",
				key:           types.NamespacedName{Name: "foo"},
				isManagedFunc: func() (bool, error) { return false, errors.New("boom") },
			},
		},
		{
			name:        "not managed, isManagedFunc does not return an error, no error expected",
			shouldError: false,
			ctrl: &clusterCatalogController{
				name:          "foo",
				key:           types.NamespacedName{Name: "foo"},
				isManagedFunc: func() (bool, error) { return false, nil },
			},
		},
		{
			name:        "managed, catalog getter error other than not found, error expected",
			shouldError: true,
			ctrl: &clusterCatalogController{
				name:          "foo",
				key:           types.NamespacedName{Name: "foo"},
				isManagedFunc: func() (bool, error) { return true, nil },
				catalogGetter: &mockCatalogGetter{
					err: errors.New("boom"),
				},
			},
		},
		{
			name:        "managed, catalog getter not found error, applier error, error expected",
			shouldError: true,
			ctrl: &clusterCatalogController{
				name:          "foo",
				key:           types.NamespacedName{Name: "foo"},
				isManagedFunc: func() (bool, error) { return true, nil },
				catalogGetter: &mockCatalogGetter{
					err: apierrors.NewNotFound(schema.GroupResource{Group: "foo", Resource: "bar"}, "baz"),
				},
				applier: &mockApplier{
					err: errors.New("boom"),
				},
				requirementsEnsurer: &mockRequirementsEnsurer{
					obj: &unstructured.Unstructured{},
				},
			},
		},
		{
			name:        "managed, catalog getter not found error, no applier error, error expected",
			shouldError: true,
			ctrl: &clusterCatalogController{
				name:          "foo",
				key:           types.NamespacedName{Name: "foo"},
				isManagedFunc: func() (bool, error) { return true, nil },
				catalogGetter: &mockCatalogGetter{
					err: apierrors.NewNotFound(schema.GroupResource{Group: "foo", Resource: "bar"}, "baz"),
				},
				applier: &mockApplier{},
				requirementsEnsurer: &mockRequirementsEnsurer{
					obj: &unstructured.Unstructured{},
				},
			},
		},
		{
			name:        "managed, catalog getter returns existing catalog, requirementsEnsurer errors, error expected",
			shouldError: true,
			ctrl: &clusterCatalogController{
				name:          "foo",
				key:           types.NamespacedName{Name: "foo"},
				isManagedFunc: func() (bool, error) { return true, nil },
				catalogGetter: &mockCatalogGetter{
					obj: &unstructured.Unstructured{},
				},
				requirementsEnsurer: &mockRequirementsEnsurer{
					err: errors.New("boom"),
				},
			},
		},
		{
			name:        "managed, catalog getter returns existing catalog, requirementsEnsurer no change, unhealthy, error expected",
			shouldError: true,
			ctrl: &clusterCatalogController{
				name:          "foo",
				key:           types.NamespacedName{Name: "foo"},
				isManagedFunc: func() (bool, error) { return true, nil },
				catalogGetter: &mockCatalogGetter{
					obj: &unstructured.Unstructured{},
				},
				requirementsEnsurer: &mockRequirementsEnsurer{
					modified: false,
				},
				healthyFunc: func(_ *unstructured.Unstructured) error {
					return errors.New("boom")
				},
			},
		},
		{
			name:        "managed, catalog getter returns existing catalog, requirementsEnsurer no change, healthy, no error expected",
			shouldError: false,
			ctrl: &clusterCatalogController{
				name:          "foo",
				key:           types.NamespacedName{Name: "foo"},
				isManagedFunc: func() (bool, error) { return true, nil },
				catalogGetter: &mockCatalogGetter{
					obj: &unstructured.Unstructured{},
				},
				requirementsEnsurer: &mockRequirementsEnsurer{
					modified: false,
				},
				healthyFunc: func(_ *unstructured.Unstructured) error {
					return nil
				},
			},
		},
		{
			name:        "managed, catalog getter returns existing catalog, requirementsEnsurer change, applier error, error expected",
			shouldError: true,
			ctrl: &clusterCatalogController{
				name:          "foo",
				key:           types.NamespacedName{Name: "foo"},
				isManagedFunc: func() (bool, error) { return true, nil },
				catalogGetter: &mockCatalogGetter{
					obj: &unstructured.Unstructured{},
				},
				applier: &mockApplier{
					err: errors.New("boom"),
				},
				requirementsEnsurer: &mockRequirementsEnsurer{
					modified: true,
				},
			},
		},
		{
			name:        "managed, catalog getter returns existing catalog, requirementsEnsurer change, no applier error, error expected",
			shouldError: true,
			ctrl: &clusterCatalogController{
				name:          "foo",
				key:           types.NamespacedName{Name: "foo"},
				isManagedFunc: func() (bool, error) { return true, nil },
				catalogGetter: &mockCatalogGetter{
					obj: &unstructured.Unstructured{},
				},
				applier: &mockApplier{},
				requirementsEnsurer: &mockRequirementsEnsurer{
					modified: true,
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

func TestMetadataSpecEnsurer(t *testing.T) {
	for _, tc := range []struct {
		name         string
		shouldModify bool
		existing     *unstructured.Unstructured
		required     *unstructured.Unstructured
		expected     *unstructured.Unstructured
	}{
        // TODO: write these tests
        {
            name: "todo",
        },
    } {
		t.Run(tc.name, func(t *testing.T) {
			mse := &metadataSpecEnsurer{
				required: tc.required,
			}

            modified, err := mse.EnsureRequirements(tc.existing)
            if err != nil {
                t.Errorf("ensuring requirements resulted in an error: %v", err)
            }

            if modified != tc.shouldModify {
                t.Errorf("modification state doesn't match expected. should modify %v, modified %v", tc.shouldModify, modified)
            }

            if !reflect.DeepEqual(tc.expected, tc.existing) {
                t.Errorf("expected and existing are not equal. expected %v, existing %v", tc.expected, tc.existing)
            }
		})
	}
}
