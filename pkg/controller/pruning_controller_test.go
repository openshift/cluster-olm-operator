package controller

import (
	"context"
	"fmt"
	configv1 "github.com/openshift/api/config/v1"
	applyoperatorv1 "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
			c := pruningController{applied: func() bool { return true }, client: client, managed: func() (bool, error) { return managed, nil }, scopes: []PruningScope{{gvr, true}}, namespaces: []string{"operands"}, desired: map[resourceIdentity]struct{}{{"", "services", "operands", "desired"}: {}}}
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
			_, err := NewPruningController("test", []PruningScope{tc.scope}, tc.namespaces, []configv1.ObjectReference{}, nil, nil, nil, nil)
			require.Error(t, err)
		})
	}
	_, err := NewPruningController("test", nil, nil, nil, nil, nil, nil, nil)
	require.ErrorContains(t, err, "complete rendered")
}

func TestPruningListFailureNeverDeletes(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "services"}
	client := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{gvr: "ServiceList"})
	client.PrependReactor("list", "services", func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, fmt.Errorf("list forbidden") })
	c := pruningController{applied: func() bool { return true }, client: client, managed: func() (bool, error) { return true, nil }, scopes: []PruningScope{{gvr, true}}, namespaces: []string{"operands"}, desired: map[resourceIdentity]struct{}{}}
	require.ErrorContains(t, c.sync(context.Background(), nil), "list forbidden")
	require.Len(t, client.Actions(), 1)
}

func TestPruningWaitsForDeploymentApply(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	stale := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]interface{}{"name": "old", "namespace": "operands", "uid": "old", "resourceVersion": "1", "labels": map[string]interface{}{ManagedByLabel: ManagedByValue}}}}
	client := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{gvr: "DeploymentList"}, stale)
	barrier := &DeploymentApplyBarrier{}
	applyClient := barrier.track(&applyStatusClient{}, "operands", "replacement")
	c := pruningController{client: client, managed: func() (bool, error) { return true, nil }, applied: barrier.Ready, scopes: []PruningScope{{gvr, true}}, namespaces: []string{"operands"}, desired: map[resourceIdentity]struct{}{{"apps", "deployments", "operands", "replacement"}: {}}}
	require.ErrorIs(t, c.sync(context.Background(), nil), errDeletionPending)
	require.Empty(t, client.Actions(), "pruning must not start before successful replacement apply")
	require.NoError(t, applyClient.ApplyOperatorStatus(context.Background(), "controller", applyoperatorv1.OperatorStatus().WithGenerations(applyoperatorv1.GenerationStatus().WithGroup("apps").WithResource("deployments").WithNamespace("operands").WithName("replacement").WithLastGeneration(1))))
	require.ErrorIs(t, c.sync(context.Background(), nil), errDeletionPending)
	_, err := client.Resource(gvr).Namespace("operands").Get(context.Background(), "old", metav1.GetOptions{})
	require.True(t, apierrors.IsNotFound(err))
}

func TestPruningCatalogFinalizerBarrier(t *testing.T) {
	catalogs := schema.GroupVersionResource{Group: "olm.operatorframework.io", Version: "v1", Resource: "clustercatalogs"}
	deployments := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	for _, terminating := range []bool{false, true} {
		t.Run(fmt.Sprintf("already terminating=%t", terminating), func(t *testing.T) {
			catalog := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "olm.operatorframework.io/v1", "kind": "ClusterCatalog", "metadata": map[string]interface{}{"name": "retired", "uid": "catalog", "resourceVersion": "1", "labels": map[string]interface{}{ManagedByLabel: ManagedByValue}}}}
			catalog.SetFinalizers([]string{"test/finalizer"})
			if terminating {
				now := metav1.Now()
				catalog.SetDeletionTimestamp(&now)
			}
			deployment := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]interface{}{"name": "stale", "namespace": "operands", "uid": "deployment", "resourceVersion": "1", "labels": map[string]interface{}{ManagedByLabel: ManagedByValue}}}}
			client := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{catalogs: "ClusterCatalogList", deployments: "DeploymentList"}, catalog, deployment)
			// Simulate API-server finalizer behavior; the dynamic fake otherwise deletes immediately.
			client.PrependReactor("delete", "clustercatalogs", func(ktesting.Action) (bool, runtime.Object, error) {
				now := metav1.Now()
				catalog.SetDeletionTimestamp(&now)
				require.NoError(t, client.Tracker().Update(catalogs, catalog, ""))
				return true, nil, nil
			})
			c := pruningController{client: client, managed: func() (bool, error) { return true, nil }, applied: func() bool { return true }, scopes: []PruningScope{{catalogs, false}, {deployments, true}}, namespaces: []string{"operands"}, desired: map[resourceIdentity]struct{}{}}
			require.ErrorIs(t, c.sync(context.Background(), nil), errDeletionPending)
			require.ErrorIs(t, c.sync(context.Background(), nil), errDeletionPending)
			_, err := client.Resource(deployments).Namespace("operands").Get(context.Background(), "stale", metav1.GetOptions{})
			require.NoError(t, err)
			require.NoError(t, client.Tracker().Delete(catalogs, "", "retired"))
			require.ErrorIs(t, c.sync(context.Background(), nil), errDeletionPending)
			_, err = client.Resource(deployments).Namespace("operands").Get(context.Background(), "stale", metav1.GetOptions{})
			require.True(t, apierrors.IsNotFound(err))
		})
	}
}
