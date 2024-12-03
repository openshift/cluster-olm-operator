package controller

import (
	"context"
	"fmt"
	"os"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-olm-operator/pkg/clients"
)

func NewProxyController(name string, proxyClient *clients.ProxyClient, kubeClient kubernetes.Interface, operatorClient *clients.OperatorClient, eventRecorder events.Recorder) factory.Controller {
	c := proxyController{
		name:           name,
		kubeClient:     kubeClient,
		operatorClient: operatorClient,
		proxyClient:    proxyClient,
	}

	return factory.New().WithSync(c.sync).WithSyncDegradedOnError(operatorClient).WithInformers(operatorClient.Informer(), proxyClient.Informer()).ToController(name, eventRecorder)
}

type proxyController struct {
	name           string
	kubeClient     kubernetes.Interface
	operatorClient *clients.OperatorClient
	proxyClient    *clients.ProxyClient
}

func (c proxyController) sync(ctx context.Context, _ factory.SyncContext) error {
	logger := klog.FromContext(ctx).WithName(c.name)
	logger.V(4).Info("sync started")
	defer logger.V(4).Info("sync finished")

	changed, err := UpdateProxyEnvironment(logger, c.proxyClient)
	if err != nil {
		return err
	}

	if changed {
		// redeploy
		// HACK!
		deps := []types.NamespacedName{
			{Name: "operator-controller-controller-manager", Namespace: "openshift-operator-controller"},
			{Name: "catalogd-controller-manager", Namespace: "openshift-catalogd"},
		}
		for _, nn := range deps {
			logger.Info("deleting deployment", "name", nn.Name, "namespace", nn.Namespace)
			err := c.kubeClient.AppsV1().Deployments(nn.Namespace).Delete(ctx, nn.Name, metav1.DeleteOptions{})
			logger.Error(err, "edeleting deployment", "name", nn.Name, "namespace", nn.Namespace)
		}
	}
	return err
}

func UpdateProxyEnvironment(logger logr.Logger, pc *clients.ProxyClient) (bool, error) {
	name := types.NamespacedName{
		Name: "cluster",
	}

	objSpec, err := pc.Get(name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			changed := updateEnvironment(logger, "HTTP_PROXY", "")
			changed = updateEnvironment(logger, "HTTPS_PROXY", "") || changed
			changed = updateEnvironment(logger, "NO_PROXY", "") || changed
			return changed, nil
		}
		return false, err
	}
	proxySpec, ok := objSpec.(*configv1.Proxy)
	if !ok {
		return false, fmt.Errorf("configv1.Proxy type assertion failed for object %v", objSpec)
	}

	changed := updateEnvironment(logger, "HTTP_PROXY", proxySpec.Status.HTTPProxy)
	changed = updateEnvironment(logger, "HTTPS_PROXY", proxySpec.Status.HTTPSProxy) || changed
	changed = updateEnvironment(logger, "NO_PROXY", proxySpec.Status.NoProxy) || changed
	return changed, nil
}

// Updates the local environment and returns true if changed, false if unchanged
// if newValue is "", then the environment variables is unset
// An unset or empty variables are considered to have the same value
func updateEnvironment(logger logr.Logger, envVar, newValue string) bool {
	oldValue := os.Getenv(envVar)
	if newValue == "" {
		os.Unsetenv(envVar)
	} else {
		os.Setenv(envVar, newValue)
	}
	if newValue != oldValue {
		logger.Info("Updated environment", "key", envVar, "old", oldValue, "new", newValue)
		return true
	}
	logger.Info("Unchanged environment", "key", envVar, "value", oldValue)
	return false
}
