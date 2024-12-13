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

const (
	HTTPProxy  = "HTTP_PROXY"
	HTTPSProxy = "HTTPS_PROXY"
	NoProxy    = "NO_PROXY"
)

func NewProxyController(name string, proxyClient *clients.ProxyClient, operatorClient *clients.OperatorClient, eventRecorder events.Recorder) factory.Controller {
	c := proxyController{
		name:        name,
		proxyClient: proxyClient,
	}

	return factory.New().WithSync(c.sync).WithSyncDegradedOnError(operatorClient).WithInformers(proxyClient.Informer()).ToController(name, eventRecorder)
}

type proxyController struct {
	name        string
	proxyClient *clients.ProxyClient
}

func (c *proxyController) sync(ctx context.Context, _ factory.SyncContext) error {
	logger := klog.FromContext(ctx).WithName(c.name).V(1)
	logger.Info("sync started")
	defer logger.Info("sync finished")

	return UpdateProxyEnvironment(logger, c.proxyClient)
}

func UpdateProxyEnvironment(logger logr.Logger, pc clients.ProxyClientInterface) error {
	logger.Info("getting proxy configuration")
	proxySpec, err := pc.Get("cluster")
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("proxy configuration not found")
			updateEnvironment(logger, map[string]string{
				HTTPProxy:  "",
				HTTPSProxy: "",
				NoProxy:    "",
			})
			return nil
		}
		return fmt.Errorf("failed to get proxy: %w", err)
	}

	logger.Info("updating environment")
	updateEnvironment(logger, map[string]string{
		HTTPProxy:  proxySpec.Status.HTTPProxy,
		HTTPSProxy: proxySpec.Status.HTTPSProxy,
		NoProxy:    proxySpec.Status.NoProxy,
	})
	return nil
}

// Updates the local environment and returns true if changed, false if unchanged
// if newValue is "", then the environment variables is unset
// An unset or empty variables are considered to have the same value
func updateEnvironment(logger logr.Logger, env map[string]string) {
	for envVar, newValue := range env {
		oldValue := os.Getenv(envVar)
		if newValue == "" {
			os.Unsetenv(envVar)
		} else {
			os.Setenv(envVar, newValue)
		}
		if newValue != oldValue {
			logger.Info("Updated environment", "key", envVar, "old", oldValue, "new", newValue)
		} else {
			logger.Info("Unchanged environment", "key", envVar, "value", oldValue)
		}
	}
}
