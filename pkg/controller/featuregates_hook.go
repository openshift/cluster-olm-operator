package controller

import (
	"slices"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
)

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
