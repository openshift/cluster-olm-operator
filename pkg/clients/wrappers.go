package clients

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	configv1helpers "github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"
)

// ConfigClientWrapper wraps ConfigV1Interface to intercept ClusterOperator status updates
type ConfigClientWrapper struct {
	configv1client.ConfigV1Interface
	coClient       configv1client.ClusterOperatorInterface
	releaseVersion string
	clock          clock.PassiveClock
}

// NewConfigClientWrapper creates a new ConfigClientWrapper
func NewConfigClientWrapper(configClient configv1client.ConfigV1Interface, releaseVersion string, clock clock.PassiveClock) *ConfigClientWrapper {
	return &ConfigClientWrapper{
		ConfigV1Interface: configClient,
		coClient:          configClient.ClusterOperators(),
		releaseVersion:    releaseVersion,
		clock:             clock,
	}
}

// ClusterOperators returns wrapped ClusterOperatorInterface
func (w *ConfigClientWrapper) ClusterOperators() configv1client.ClusterOperatorInterface {
	return &coWrapper{w.coClient, w.releaseVersion, w.clock}
}

// coWrapper wraps ClusterOperatorInterface to intercept UpdateStatus calls
type coWrapper struct {
	configv1client.ClusterOperatorInterface
	releaseVersion string
	clock          clock.PassiveClock
}

// UpdateStatus intercepts status updates to detect version changes and set Progressing=True during upgrades
func (w *coWrapper) UpdateStatus(ctx context.Context, co *configv1.ClusterOperator, opts metav1.UpdateOptions) (*configv1.ClusterOperator, error) {
	if w.releaseVersion != "" {
		// Get current ClusterOperator to compare versions
		original, err := w.ClusterOperatorInterface.Get(ctx, co.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}

		// Check if RELEASE_VERSION exists in ClusterOperator.Status.Versions
		for _, v := range original.Status.Versions {
			if v.Version == w.releaseVersion {
				// Version matches, and so we are not in an upgrade
				return w.ClusterOperatorInterface.UpdateStatus(ctx, co, opts)
			}
		}
		// If RELEASE_VERSION not found, then we are in an upgrade, and so set Progressing to True
		klog.Infof("Version change detected, setting Progressing=True for version %s", w.releaseVersion)
		configv1helpers.SetStatusCondition(&co.Status.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:    configv1.OperatorProgressing,
			Status:  configv1.ConditionTrue,
			Reason:  "UpgradeInProgress",
			Message: fmt.Sprintf("Progressing towards operator version %s", w.releaseVersion),
		}, w.clock)
	}

	return w.ClusterOperatorInterface.UpdateStatus(ctx, co, opts)
}
