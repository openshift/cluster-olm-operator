package controller

import (
	"context"
	"fmt"
	"testing"

	applyoperatorv1 "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"github.com/stretchr/testify/require"
)

type applyStatusClient struct {
	v1helpers.OperatorClientWithFinalizers
	err error
}

func (c *applyStatusClient) ApplyOperatorStatus(context.Context, string, *applyoperatorv1.OperatorStatusApplyConfiguration) error {
	return c.err
}

func TestDeploymentApplyBarrier(t *testing.T) {
	barrier := &DeploymentApplyBarrier{}
	delegate := &applyStatusClient{}
	first := barrier.track(delegate, "operands", "replacement")
	second := barrier.track(delegate, "operands", "other")
	require.False(t, barrier.Ready())
	status := func(name string) *applyoperatorv1.OperatorStatusApplyConfiguration {
		return applyoperatorv1.OperatorStatus().WithGenerations(applyoperatorv1.GenerationStatus().WithGroup("apps").WithResource("deployments").WithNamespace("operands").WithName(name).WithLastGeneration(1))
	}
	require.NoError(t, first.ApplyOperatorStatus(context.Background(), "controller", status("old-name")))
	require.False(t, barrier.Ready())
	require.NoError(t, first.ApplyOperatorStatus(context.Background(), "controller", status("replacement")))
	require.False(t, barrier.Ready())
	delegate.err = fmt.Errorf("status update failed")
	require.Error(t, second.ApplyOperatorStatus(context.Background(), "controller", status("other")))
	require.False(t, barrier.Ready())
	delegate.err = nil
	require.NoError(t, second.ApplyOperatorStatus(context.Background(), "controller", status("other")))
	require.True(t, barrier.Ready())
}
