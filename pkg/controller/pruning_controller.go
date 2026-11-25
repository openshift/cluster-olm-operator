package controller

import (
	"context"
	"fmt"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-olm-operator/pkg/clients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// PruningScope remains configured even when a kind disappears from the chart.
// Namespaced scopes are listed only in the namespaces supplied to the controller.
type PruningScope struct {
	Resource   schema.GroupVersionResource
	Namespaced bool
}

type resourceIdentity struct{ group, resource, namespace, name string }

type pruningController struct {
	client     dynamic.Interface
	managed    managedFunc
	scopes     []PruningScope
	namespaces []string
	desired    map[resourceIdentity]struct{}
}

// NewPruningController requires a complete desired set from a successful build.
// Diagnostic relatedObjects must never be passed as the desired set. Polling
// bounds API usage to these resource types and namespaces without new watches.
func NewPruningController(name string, scopes []PruningScope, namespaces []string, desired []configv1.ObjectReference, operatorClient *clients.OperatorClient, client dynamic.Interface, recorder events.Recorder) (factory.Controller, error) {
	if desired == nil {
		return nil, fmt.Errorf("pruning requires a complete rendered resource set")
	}
	for _, ns := range namespaces {
		if ns == "" {
			return nil, fmt.Errorf("pruning namespace must not be empty")
		}
	}
	for _, s := range scopes {
		if s.Resource.Resource == "" || s.Resource.Version == "" {
			return nil, fmt.Errorf("invalid pruning scope: %+v", s)
		}
		if (s.Resource.Group == "" && s.Resource.Resource == "namespaces") || (s.Resource.Group == "apiextensions.k8s.io" && s.Resource.Resource == "customresourcedefinitions") {
			return nil, fmt.Errorf("pruning namespaces and CRDs is prohibited")
		}
	}
	c := &pruningController{client: client, managed: cleanupManagedFunc(operatorClient), scopes: append([]PruningScope(nil), scopes...), namespaces: append([]string(nil), namespaces...), desired: map[resourceIdentity]struct{}{}}
	for _, d := range desired {
		c.desired[resourceIdentity{d.Group, d.Resource, d.Namespace, d.Name}] = struct{}{}
	}
	return factory.New().WithSync(withCleanupStatus(name, operatorClient, c.sync)).WithSyncDegradedOnError(operatorClient).WithInformers(operatorClient.Informer()).ResyncEvery(time.Minute).ToController(name, recorder), nil
}

func (c *pruningController) sync(ctx context.Context, _ factory.SyncContext) error {
	managed, err := c.managed()
	if err != nil {
		return err
	}
	if !managed {
		return nil
	}
	// Process scopes in order. Pending custom-resource finalizers stop cleanup
	// before the deployments responsible for completing those finalizers disappear.
	for _, scope := range c.scopes {
		namespaces := []string{""}
		if scope.Namespaced {
			namespaces = c.namespaces
		}
		for _, ns := range namespaces {
			var ri dynamic.ResourceInterface = c.client.Resource(scope.Resource)
			if scope.Namespaced {
				ri = c.client.Resource(scope.Resource).Namespace(ns)
			}
			list, err := ri.List(ctx, metav1.ListOptions{LabelSelector: ManagedByLabel + "=" + ManagedByValue})
			if err != nil {
				return fmt.Errorf("listing pruning scope %s in %q: %w", scope.Resource, ns, err)
			}
			for _, obj := range list.Items {
				if obj.GetLabels()[ManagedByLabel] != ManagedByValue {
					continue
				}
				if _, ok := c.desired[resourceIdentity{scope.Resource.Group, scope.Resource.Resource, obj.GetNamespace(), obj.GetName()}]; ok {
					continue
				}
				if obj.GetDeletionTimestamp() != nil {
					return fmt.Errorf("%w: %s %s/%s", errDeletionPending, scope.Resource, ns, obj.GetName())
				}
				uid, rv := obj.GetUID(), obj.GetResourceVersion()
				if uid == "" || rv == "" {
					return fmt.Errorf("pruning resource %s lacks UID or resourceVersion", obj.GetName())
				}
				// resourceVersion also protects an ownership label removed since LIST.
				err = ri.Delete(ctx, obj.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}})
				if apierrors.IsNotFound(err) {
					continue
				}
				if err != nil {
					return fmt.Errorf("pruning %s %s/%s: %w", scope.Resource, ns, obj.GetName(), err)
				}
				return fmt.Errorf("%w: %s %s/%s", errDeletionPending, scope.Resource, ns, obj.GetName())
			}
		}
	}
	return nil
}

// DefaultPruningScopes deliberately excludes namespaces, CRDs and operand-owned
// resources (such as ClusterExtensions and ClusterObjectSets). Keep retired kinds
// here so objects of a kind removed entirely from the chart can still be found.
func DefaultPruningScopes() []PruningScope {
	return []PruningScope{
		{schema.GroupVersionResource{Group: "olm.operatorframework.io", Version: "v1", Resource: "clustercatalogs"}, false},
		{schema.GroupVersionResource{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingwebhookconfigurations"}, false},
		{schema.GroupVersionResource{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "mutatingwebhookconfigurations"}, false},
		{schema.GroupVersionResource{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingadmissionpolicies"}, false},
		{schema.GroupVersionResource{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingadmissionpolicybindings"}, false},
		{schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, true},
		{schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, true},
		{schema.GroupVersionResource{Version: "v1", Resource: "secrets"}, true},
		{schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}, true},
		{schema.GroupVersionResource{Group: "", Version: "v1", Resource: "serviceaccounts"}, true},
		{schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"}, true},
		{schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"}, true},
		{schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}, false},
		{schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}, false},
		{schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}, true},
		{schema.GroupVersionResource{Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"}, true},
		{schema.GroupVersionResource{Group: "monitoring.coreos.com", Version: "v1", Resource: "servicemonitors"}, true},
	}
}
