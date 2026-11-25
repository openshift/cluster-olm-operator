package controller

import (
	"context"
	"fmt"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
	"testing"
)

func TestResourceDeletionController(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "olm.operatorframework.io", Version: "v1", Resource: "clustercatalogs"}
	for _, tc := range []struct {
		name                 string
		managed, terminating bool
	}{{"legacy unmarked resource", true, false}, {"unmanaged", false, false}, {"finalizing", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			obj := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "olm.operatorframework.io/v1", "kind": "ClusterCatalog", "metadata": map[string]interface{}{"name": "retired", "uid": "original"}}}
			if tc.terminating {
				now := metav1.Now()
				obj.SetDeletionTimestamp(&now)
			}
			client := fake.NewSimpleDynamicClient(runtime.NewScheme(), obj)
			client.PrependReactor("delete", "clustercatalogs", func(a ktesting.Action) (bool, runtime.Object, error) {
				require.Equal(t, types.UID("original"), *a.(ktesting.DeleteAction).GetDeleteOptions().Preconditions.UID)
				return false, nil, nil
			})
			c := resourceDeletionController{resources: []ResourceToDelete{{Resource: gvr, Name: "retired"}}, client: client, managed: func() (bool, error) { return tc.managed, nil }}
			err := c.sync(context.Background(), factory.SyncContext(nil))
			if !tc.managed {
				require.NoError(t, err)
				require.Empty(t, client.Actions())
				return
			}
			require.Error(t, err)
			if tc.terminating {
				require.Len(t, client.Actions(), 1)
				return
			}
			require.NoError(t, c.sync(context.Background(), nil))
		})
	}
}

func TestRetirementsContinuePastPendingEntry(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "olm.operatorframework.io", Version: "v1", Resource: "clustercatalogs"}
	for _, failSecond := range []bool{false, true} {
		t.Run(fmt.Sprintf("second API failure=%t", failSecond), func(t *testing.T) {
			first := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "olm.operatorframework.io/v1", "kind": "ClusterCatalog", "metadata": map[string]interface{}{"name": "first", "uid": "first"}}}
			now := metav1.Now()
			first.SetDeletionTimestamp(&now)
			first.SetFinalizers([]string{"test/finalizer"})
			second := first.DeepCopy()
			second.SetName("second")
			second.SetUID("second")
			second.SetDeletionTimestamp(nil)
			second.SetFinalizers(nil)
			client := fake.NewSimpleDynamicClient(runtime.NewScheme(), first, second)
			if failSecond {
				client.PrependReactor("delete", "clustercatalogs", func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, fmt.Errorf("delete forbidden") })
			}
			c := resourceDeletionController{resources: []ResourceToDelete{{Resource: gvr, Name: "first"}, {Resource: gvr, Name: "second"}}, client: client, managed: func() (bool, error) { return true, nil }}
			err := c.sync(context.Background(), nil)
			if failSecond {
				require.ErrorContains(t, err, "delete forbidden")
				require.NotErrorIs(t, err, errDeletionPending)
			} else {
				require.ErrorIs(t, err, errDeletionPending)
				_, err = client.Resource(gvr).Get(context.Background(), "second", metav1.GetOptions{})
				require.True(t, apierrors.IsNotFound(err))
			}
		})
	}
}
