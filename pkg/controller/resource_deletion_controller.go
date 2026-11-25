package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/openshift/cluster-olm-operator/pkg/clients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// ResourceToDelete is an explicit retirement instruction, including for legacy
// objects that predate ownership labels. Entries must never overlap desired objects.
type ResourceToDelete struct {
	Resource  schema.GroupVersionResource
	Namespace string
	Name      string
}

func NewResourceDeletionController(name string, resources []ResourceToDelete, operatorClient *clients.OperatorClient, client dynamic.Interface, recorder events.Recorder) factory.Controller {
	c := &resourceDeletionController{resources: append([]ResourceToDelete(nil), resources...), client: client, managed: cleanupManagedFunc(operatorClient)}
	return factory.New().WithSync(withCleanupStatus(name, operatorClient, c.sync)).WithSyncDegradedOnError(operatorClient).WithInformers(operatorClient.Informer()).ResyncEvery(time.Minute).ToController(name, recorder)
}

type resourceDeletionController struct {
	resources []ResourceToDelete
	client    dynamic.Interface
	managed   managedFunc
}

func (c *resourceDeletionController) sync(ctx context.Context, _ factory.SyncContext) error {
	managed, err := c.managed()
	if err != nil {
		return err
	}
	if !managed {
		return nil
	}
	var pending error
	for _, ref := range c.resources {
		if ref.Name == "" || ref.Resource.Resource == "" || ref.Resource.Version == "" {
			return fmt.Errorf("invalid resource retirement reference: %+v", ref)
		}
		if err := deleteRetiredResource(ctx, c.client, ref); err != nil {
			if errors.Is(err, errDeletionPending) {
				pending = err
				continue
			}
			return err
		}
	}
	return pending
}

func deleteRetiredResource(ctx context.Context, client dynamic.Interface, ref ResourceToDelete) error {
	var ri dynamic.ResourceInterface = client.Resource(ref.Resource)
	if ref.Namespace != "" {
		ri = client.Resource(ref.Resource).Namespace(ref.Namespace)
	}
	obj, err := ri.Get(ctx, ref.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("getting retired resource %s %s/%s: %w", ref.Resource, ref.Namespace, ref.Name, err)
	}
	if obj.GetDeletionTimestamp() != nil {
		return fmt.Errorf("%w: %s %s/%s", errDeletionPending, ref.Resource, ref.Namespace, ref.Name)
	}
	uid := obj.GetUID()
	if uid == "" {
		return fmt.Errorf("retired resource %s has no UID", ref.Name)
	}
	err = ri.Delete(ctx, ref.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("deleting retired resource %s: %w", ref.Name, err)
	}
	return fmt.Errorf("%w: %s %s/%s", errDeletionPending, ref.Resource, ref.Namespace, ref.Name)
}
