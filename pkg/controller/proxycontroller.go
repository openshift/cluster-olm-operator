package controller

import (
	"context"
	"fmt"
	"os"

	"github.com/go-logr/logr"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-olm-operator/pkg/clients"
)

func NewProxyController(name string, proxyClient *clients.ProxyClient, operatorClient *clients.OperatorClient, eventRecorder events.Recorder) factory.Controller {
	c := proxyController{
		name:        name,
		proxyClient: proxyClient,
	}

	return factory.New().WithSync(c.sync).WithSyncDegradedOnError(operatorClient).WithInformers(operatorClient.Informer(), proxyClient.Informer()).ToController(name, eventRecorder)
}

type proxyController struct {
	name        string
	proxyClient *clients.ProxyClient
}

func (c *proxyController) sync(ctx context.Context, _ factory.SyncContext) error {
	logger := klog.FromContext(ctx).WithName(c.name)
	logger.V(4).Info("sync started")
	defer logger.V(4).Info("sync finished")

	return UpdateProxyEnvironment(logger, c.proxyClient)
}

func UpdateProxyEnvironment(logger logr.Logger, pc *clients.ProxyClient) error {
	logger.Info("getting proxy configuration")
	proxySpec, err := pc.Get("cluster")
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("proxy configuration not found")
			updateEnvironment(logger, "HTTP_PROXY", "")
			updateEnvironment(logger, "HTTPS_PROXY", "")
			updateEnvironment(logger, "NO_PROXY", "")
			return nil
		}
		return fmt.Errorf("failed to get proxy: %w", err)
	}

	logger.Info("updating environment")
	updateEnvironment(logger, "HTTP_PROXY", proxySpec.Status.HTTPProxy)
	updateEnvironment(logger, "HTTPS_PROXY", proxySpec.Status.HTTPSProxy)
	updateEnvironment(logger, "NO_PROXY", proxySpec.Status.NoProxy)
	return nil
}

// Updates the local environment and returns true if changed, false if unchanged
// if newValue is "", then the environment variables is unset
// An unset or empty variables are considered to have the same value
func updateEnvironment(logger logr.Logger, envVar, newValue string) {
	oldValue := os.Getenv(envVar)
	if newValue == "" {
		os.Unsetenv(envVar)
	} else {
		os.Setenv(envVar, newValue)
	}
	if newValue != oldValue {
		logger.Info("Updated environment", "key", envVar, "old", oldValue, "new", newValue)
	}
	logger.Info("Unchanged environment", "key", envVar, "value", oldValue)
}
