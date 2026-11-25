package controller

import (
	"context"
	"sync"

	applyoperatorv1 "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

// DeploymentApplyBarrier is local to a render. Only successful apply/status
// completion from this process can release it; old cluster status cannot.
type DeploymentApplyBarrier struct {
	mu      sync.Mutex
	pending map[string]bool
}

func (b *DeploymentApplyBarrier) Ready() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, pending := range b.pending {
		if pending {
			return false
		}
	}
	return true
}

func (b *DeploymentApplyBarrier) track(client v1helpers.OperatorClientWithFinalizers, namespace, name string) v1helpers.OperatorClientWithFinalizers {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pending == nil {
		b.pending = map[string]bool{}
	}
	key := namespace + "/" + name
	b.pending[key] = true
	return &deploymentApplyClient{OperatorClientWithFinalizers: client, barrier: b, namespace: namespace, name: name, key: key}
}

type deploymentApplyClient struct {
	v1helpers.OperatorClientWithFinalizers
	barrier              *DeploymentApplyBarrier
	namespace, name, key string
}

// library-go writes the applied Deployment generation only after ApplyDeployment
// succeeds (including a successful no-op apply). Use that completion handoff,
// rather than controller startup or status left by a previous operator process.
func (c *deploymentApplyClient) ApplyOperatorStatus(ctx context.Context, manager string, status *applyoperatorv1.OperatorStatusApplyConfiguration) error {
	if err := c.OperatorClientWithFinalizers.ApplyOperatorStatus(ctx, manager, status); err != nil {
		return err
	}
	for _, generation := range status.Generations {
		if generation.Group != nil && *generation.Group == "apps" && generation.Resource != nil && *generation.Resource == "deployments" && generation.Namespace != nil && *generation.Namespace == c.namespace && generation.Name != nil && *generation.Name == c.name && generation.LastGeneration != nil {
			c.barrier.mu.Lock()
			c.barrier.pending[c.key] = false
			c.barrier.mu.Unlock()
		}
	}
	return nil
}
