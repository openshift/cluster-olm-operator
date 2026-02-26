package clients

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	configv1helpers "github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"
)

// ConfigClientWrapper wraps ConfigV1Interface to intercept ClusterOperator status updates
type ConfigClientWrapper struct {
	configv1client.ConfigV1Interface
	coClient       configv1client.ClusterOperatorInterface
	coLister       configv1listers.ClusterOperatorLister
	releaseVersion string
	clock          clock.PassiveClock
}

// NewConfigClientWrapper creates a new ConfigClientWrapper
func NewConfigClientWrapper(configClient configv1client.ConfigV1Interface, coLister configv1listers.ClusterOperatorLister, releaseVersion string, clock clock.PassiveClock) *ConfigClientWrapper {
	return &ConfigClientWrapper{
		ConfigV1Interface: configClient,
		coClient:          configClient.ClusterOperators(),
		coLister:          coLister,
		releaseVersion:    releaseVersion,
		clock:             clock,
	}
}

// ClusterOperators returns wrapped ClusterOperatorInterface
func (w *ConfigClientWrapper) ClusterOperators() configv1client.ClusterOperatorInterface {
	return &coWrapper{w.coClient, w.coLister, w.releaseVersion, w.clock}
}

// coWrapper wraps ClusterOperatorInterface to intercept UpdateStatus calls
type coWrapper struct {
	configv1client.ClusterOperatorInterface
	coLister       configv1listers.ClusterOperatorLister
	releaseVersion string
	clock          clock.PassiveClock
}

// UpdateStatus intercepts status updates to detect version changes and set Progressing=True during upgrades
func (w *coWrapper) UpdateStatus(ctx context.Context, co *configv1.ClusterOperator, opts metav1.UpdateOptions) (*configv1.ClusterOperator, error) {
	if w.releaseVersion == "" {
		return w.ClusterOperatorInterface.UpdateStatus(ctx, co, opts)
	}

	// Get current ClusterOperator from cache to compare versions
	original, err := w.coLister.Get(co.Name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return w.ClusterOperatorInterface.UpdateStatus(ctx, co, opts)
		}
		return nil, err
	}

	// Check if RELEASE_VERSION exists in ClusterOperator.Status.Versions with name "operator"
	for _, v := range original.Status.Versions {
		if v.Name == "operator" && v.Version == w.releaseVersion {
			// Operator version matches, and so we are not in an upgrade
			return w.ClusterOperatorInterface.UpdateStatus(ctx, co, opts)
		}
	}

	// If RELEASE_VERSION not found and Status.Versions is not empty, then we are in an upgrade
	if len(original.Status.Versions) > 0 {
		klog.V(4).Infof("Version change detected, setting Progressing=True for version %s", w.releaseVersion)
		configv1helpers.SetStatusCondition(&co.Status.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:    configv1.OperatorProgressing,
			Status:  configv1.ConditionTrue,
			Reason:  "UpgradeInProgress",
			Message: fmt.Sprintf("Progressing towards operator version %s", w.releaseVersion),
		}, w.clock)
	}

	return w.ClusterOperatorInterface.UpdateStatus(ctx, co, opts)
}
