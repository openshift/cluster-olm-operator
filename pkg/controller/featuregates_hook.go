package controller

import (
	"errors"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"

	"github.com/openshift/cluster-olm-operator/pkg/helmvalues"
)

// upstreamFeatureGates builds a set of helm values for the downsteeam feature-gates that are
// mapped to upstream feature-gates
func upstreamFeatureGates(
	values *helmvalues.HelmValues,
	clusterGatesConfig featuregates.FeatureGate,
	downstreamGates []configv1.FeatureGateName,
	downstreamToUpstreamFunc func(configv1.FeatureGateName) func(*helmvalues.HelmValues, bool) error,
) (*helmvalues.HelmValues, error) {
	errs := make([]error, 0, len(downstreamGates))

	for _, downstreamGate := range downstreamGates {
		f := downstreamToUpstreamFunc(downstreamGate)
		errs = append(errs, f(values, clusterGatesConfig.Enabled(downstreamGate)))
	}

	return values, errors.Join(errs...)
}
