package controller

import (
	"context"
	"fmt"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-olm-operator/pkg/clients"
)

func NewStaticUpgradeableConditionController(name string, operatorClient *clients.OperatorClient, eventRecorder events.Recorder, prefixes []string) factory.Controller {
	c := staticUpgradeableConditionController{
		name:           name,
		operatorClient: operatorClient,
		prefixes:       prefixes,
	}

	return factory.New().WithSync(c.sync).WithSyncDegradedOnError(operatorClient).WithInformers(operatorClient.Informer()).ToController(name, eventRecorder)
}

type staticUpgradeableConditionController struct {
	name           string
	operatorClient *clients.OperatorClient
	prefixes       []string
}

func (c staticUpgradeableConditionController) sync(ctx context.Context, _ factory.SyncContext) error {
	logger := klog.FromContext(ctx).WithName(c.name)
	logger.V(4).Info("sync started")
	defer logger.V(4).Info("sync finished")

	opSpec, _, _, err := c.operatorClient.GetOperatorState()
	if err != nil {
		return err
	}
	if opSpec.ManagementState != operatorv1.Managed {
		return nil
	}

	updateStatusFuncs := make([]v1helpers.UpdateStatusFunc, 0, len(c.prefixes))
	for _, prefix := range c.prefixes {
		updateStatusFuncs = append(updateStatusFuncs, v1helpers.UpdateConditionFn(operatorv1.OperatorCondition{
			Type:   fmt.Sprintf("%sUpgradeable", prefix),
			Status: operatorv1.ConditionTrue,
		}))
	}

	if _, _, updateErr := v1helpers.UpdateStatus(ctx, c.operatorClient, updateStatusFuncs...); updateErr != nil {
		return updateErr
	}

	return nil
}
