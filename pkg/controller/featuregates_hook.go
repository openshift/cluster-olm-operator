package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	internalfeatures "github.com/openshift/cluster-olm-operator/internal/featuregates"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/deploymentcontroller"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/klog/v2"
)

const (
	operatorControllerDeploymentName = "operator-controller-controller-manager"
	catalogdDeploymentName           = "catalogd-controller-manager"
)

// UpdateDeploymentFeatureGatesHook handles setting --feature-gates container argument
// on 'manager' containers for both OLM deployments - catalogd and operator-controller
// with appropriate enabled feature gates
func UpdateDeploymentFeatureGatesHook(
	featuresAccessor featuregates.FeatureGateAccess,
	featuresMapper internalfeatures.MapperInterface,
	featureSet configv1.FeatureSet,
) deploymentcontroller.DeploymentHookFunc {
	return func(_ *operatorv1.OperatorSpec, deployment *appsv1.Deployment) error {
		logger := klog.FromContext(context.Background()).WithName("feature_gates_hook")
		logger.V(0).Info("updating environment", "deployment", deployment.Name)

		clusterGatesConfig, err := featuresAccessor.CurrentFeatureGates()
		if err != nil {
			return fmt.Errorf("error getting featuregates.config.openshift.io/cluster: %w", err)
		}

		var upstreamGates []string
		switch deployment.Name {
		case operatorControllerDeploymentName:
			upstreamGates = upstreamFeatureGates(
				clusterGatesConfig,
				featuresMapper.OperatorControllerDownstreamFeatureGates(),
				featuresMapper.OperatorControllerUpstreamForDownstream,
			)
		case catalogdDeploymentName:
			upstreamGates = upstreamFeatureGates(
				clusterGatesConfig,
				featuresMapper.CatalogdDownstreamFeatureGates(),
				featuresMapper.CatalogdUpstreamForDownstream,
			)
		default:
			logger.V(4).Info("unrecognized deployment", "deployment", deployment.Name)
			return nil
		}
		logger.V(4).Info("enabled feature gates", "feature gates", upstreamGates, "deployment", deployment.Name)

		featureGatesToSet := internalfeatures.FormatAsEnabledArgs(upstreamGates)
		var errs []error
		for i := range deployment.Spec.Template.Spec.Containers {
			logger.V(4).Info("iterating containers", "container", deployment.Spec.Template.Spec.Containers[i].Name, "deployment", deployment.Name)
			if !strings.EqualFold(deployment.Spec.Template.Spec.Containers[i].Name, "manager") {
				continue
			}

			// If featureSet is not found in AllFixedFeatureSets, then it is Custom, so we need to ignore mismatches
			ignoreMismatch := slices.Index(configv1.AllFixedFeatureSets, featureSet) == -1
			if err := setContainerFeatureGateArg(&deployment.Spec.Template.Spec.Containers[i], featureGatesToSet, ignoreMismatch); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) > 0 {
			return errors.Join(errs...)
		}

		return nil
	}
}

// upstreamFeatureGates build and returns a unique and ordered list of upstream feature gates names
// that map to the provided enabled downstream feature gates
func upstreamFeatureGates(
	clusterGatesConfig featuregates.FeatureGate,
	downstreamGates []configv1.FeatureGateName,
	downstreamToUpstreamFunc func(configv1.FeatureGateName) []string,
) []string {
	var upstreamGates []string

	seen := make(map[string]struct{})
	for _, downstreamGate := range downstreamGates {
		if !clusterGatesConfig.Enabled(downstreamGate) {
			continue
		}

		for _, upstreamGate := range downstreamToUpstreamFunc(downstreamGate) {
			if _, found := seen[upstreamGate]; found {
				continue
			}

			seen[upstreamGate] = struct{}{}
			upstreamGates = append(upstreamGates, upstreamGate)
		}
	}
	slices.Sort(upstreamGates)

	return upstreamGates
}
