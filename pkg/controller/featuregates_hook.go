package controller

import (
	"errors"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-olm-operator/pkg/helmvalues"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
)

// upstreamFeatureGates builds a set of helm values for the downsteeam feature-gates that are
// mapped to upstream feature-gates
func upstreamFeatureGates(
	clusterGatesConfig featuregates.FeatureGate,
	downstreamGates []configv1.FeatureGateName,
	downstreamToUpstreamFunc func(configv1.FeatureGateName) func(*helmvalues.HelmValues, bool) error,
) (*helmvalues.HelmValues, error) {
	errs := make([]error, 0, len(downstreamGates))
	values := helmvalues.NewHelmValues()

	for _, downstreamGate := range downstreamGates {
		f := downstreamToUpstreamFunc(downstreamGate)
		errs = append(errs, f(values, clusterGatesConfig.Enabled(downstreamGate)))
	}

	return values, errors.Join(errs...)
}
