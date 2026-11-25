package controller

import (
	"context"
	"fmt"
	configv1 "github.com/openshift/api/config/v1"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestPruningOwnershipAndScope(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "services"}
	object := func(ns, name string, owned bool) *unstructured.Unstructured {
		obj := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "v1", "kind": "Service", "metadata": map[string]interface{}{"name": name, "namespace": ns, "uid": name, "resourceVersion": "1"}}}
		if owned {
			obj.SetLabels(map[string]string{ManagedByLabel: ManagedByValue})
		}
		return obj
	}
	for _, managed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unmanaged", true: "managed"}[managed], func(t *testing.T) {
			client := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{gvr: "ServiceList"}, object("operands", "desired", true), object("operands", "stale", true), object("operands", "user", false), object("elsewhere", "stale", true))
			client.PrependReactor("delete", "services", func(a ktesting.Action) (bool, runtime.Object, error) {
				opts := a.(ktesting.DeleteAction).GetDeleteOptions()
				require.Equal(t, types.UID("stale"), *opts.Preconditions.UID)
				require.Equal(t, "1", *opts.Preconditions.ResourceVersion)
				return false, nil, nil
			})
			c := pruningController{client: client, managed: func() (bool, error) { return managed, nil }, scopes: []PruningScope{{gvr, true}}, namespaces: []string{"operands"}, desired: map[resourceIdentity]struct{}{{"", "services", "operands", "desired"}: {}}}
			err := c.sync(context.Background(), nil)
			if !managed {
				require.NoError(t, err)
				require.Empty(t, client.Actions())
				return
			}
			require.Error(t, err) // Wait for confirmed disappearance.
			require.NoError(t, c.sync(context.Background(), nil))
			for _, name := range []string{"desired", "user"} {
				_, err := client.Resource(gvr).Namespace("operands").Get(context.Background(), name, metav1.GetOptions{})
				require.NoError(t, err)
			}
			_, err = client.Resource(gvr).Namespace("elsewhere").Get(context.Background(), "stale", metav1.GetOptions{})
			require.NoError(t, err)
			list := client.Actions()[0].(ktesting.ListAction)
			require.Equal(t, "operands", list.GetNamespace())
			require.Equal(t, ManagedByLabel+"="+ManagedByValue, list.GetListRestrictions().Labels.String())
		})
	}
}

func TestPruningRejectsUnsafeConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name       string
		scope      PruningScope
		namespaces []string
	}{
		{name: "empty namespace", namespaces: []string{""}},
		{name: "namespace deletion", scope: PruningScope{Resource: schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}}},
		{name: "CRD deletion", scope: PruningScope{Resource: schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewPruningController("test", []PruningScope{tc.scope}, tc.namespaces, []configv1.ObjectReference{}, nil, nil, nil)
			require.Error(t, err)
		})
	}
	_, err := NewPruningController("test", nil, nil, nil, nil, nil, nil)
	require.ErrorContains(t, err, "complete rendered")
}

func TestPruningListFailureNeverDeletes(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "services"}
	client := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{gvr: "ServiceList"})
	client.PrependReactor("list", "services", func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, fmt.Errorf("list forbidden") })
	c := pruningController{client: client, managed: func() (bool, error) { return true, nil }, scopes: []PruningScope{{gvr, true}}, namespaces: []string{"operands"}, desired: map[resourceIdentity]struct{}{}}
	require.ErrorContains(t, c.sync(context.Background(), nil), "list forbidden")
	require.Len(t, client.Actions(), 1)
}
