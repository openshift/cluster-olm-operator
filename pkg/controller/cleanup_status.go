package controller

import (
	"context"
	"errors"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-olm-operator/pkg/clients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

var errDeletionPending = errors.New("resource deletion pending")

// Cleanup must not interpret Removed as permission to prune chart resources.
func cleanupManagedFunc(client *clients.OperatorClient) managedFunc {
	return func() (bool, error) {
		spec, _, _, err := client.GetOperatorState()
		if err != nil {
			return false, err
		}
		return spec.ManagementState == operatorv1.Managed, nil
	}
}

// A finalizer is normal asynchronous progress, not a degraded reconciliation.
func withCleanupStatus(name string, client *clients.OperatorClient, sync factory.SyncFunc) factory.SyncFunc {
	return func(ctx context.Context, sc factory.SyncContext) error {
		err := sync(ctx, sc)
		pending := errors.Is(err, errDeletionPending)
		condition := operatorv1.OperatorCondition{Type: name + "Progressing", Status: operatorv1.ConditionFalse, Reason: "AsExpected"}
		if pending {
			condition.Status = operatorv1.ConditionTrue
			condition.Reason = "DeletionPending"
			condition.Message = err.Error()
			sc.Queue().AddAfter(sc.QueueKey(), 5*time.Second)
			err = nil
		}
		if _, _, statusErr := v1helpers.UpdateStatus(ctx, client, v1helpers.UpdateConditionFn(condition)); statusErr != nil {
			return errors.Join(err, statusErr)
		}
		return err
	}
}
