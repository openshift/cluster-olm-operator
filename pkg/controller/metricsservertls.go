package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	"github.com/openshift/library-go/pkg/crypto"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"
)

// GetMetricsServerTLSServingInfo reads the cluster TLS security profile from the APIServer
// config and returns a configv1.HTTPServingInfo populated with MinTLSVersion,
// CipherSuites, and CurvePreferences.
// Returns an empty HTTPServingInfo (with no error) if the APIServer resource is not found.
func GetMetricsServerTLSServingInfo(ctx context.Context, configClient configclient.Interface) (configv1.HTTPServingInfo, error) {
	apiServer, err := configClient.ConfigV1().APIServers().Get(ctx, "cluster", metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return configv1.HTTPServingInfo{}, nil
	}
	if err != nil {
		return configv1.HTTPServingInfo{}, fmt.Errorf("error reading APIServer config: %w", err)
	}

	minTLSVersion, cipherSuites, curvePreferences := tlsSettingsFromProfile(apiServer.Spec.TLSSecurityProfile)
	return configv1.HTTPServingInfo{
		ServingInfo: configv1.ServingInfo{
			MinTLSVersion:    minTLSVersion,
			CipherSuites:     cipherSuites,
			CurvePreferences: curvePreferences,
		},
	}, nil
}

// tlsSettingsFromProfile extracts the minimum TLS version, IANA cipher suite names,
// and Go CurveIDs from a TLSSecurityProfile. It mirrors library-go's TLS profile
// resolution while converting API TLSGroup values to the form used by the metrics
// server's SecureServingOptions.
func tlsSettingsFromProfile(profile *configv1.TLSSecurityProfile) (string, []string, []int32) {
	profileType := crypto.DefaultTLSProfileType
	if profile != nil {
		profileType = profile.Type
	}

	var profileSpec *configv1.TLSProfileSpec
	if profileType == configv1.TLSProfileCustomType {
		if profile != nil && profile.Custom != nil {
			profileSpec = &profile.Custom.TLSProfileSpec
		}
	} else {
		profileSpec = configv1.TLSProfiles[profileType]
	}

	if profileSpec == nil {
		profileSpec = configv1.TLSProfiles[crypto.DefaultTLSProfileType]
	}

	return string(profileSpec.MinTLSVersion), crypto.OpenSSLToIANACipherSuites(profileSpec.Ciphers), curvePreferencesFromTLSGroups(profileSpec.Groups)
}

// TLSProfileFromObservedConfig extracts the TLS settings stored in the operator's
// observedConfig at the olmTLSSecurityProfile paths. Curve preference names are
// converted to the numeric Go CurveIDs used by the metrics server. It returns empty
// values if the observedConfig is absent or unparseable.
func TLSProfileFromObservedConfig(operatorSpec *operatorv1.OperatorSpec) (string, []string, []int32) {
	if operatorSpec == nil || len(operatorSpec.ObservedConfig.Raw) == 0 {
		return "", nil, nil
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(operatorSpec.ObservedConfig.Raw, &cfg); err != nil {
		return "", nil, nil
	}
	minTLS, _, err := unstructured.NestedString(cfg, TLSMinVersionPath()...)
	if err != nil {
		return "", nil, nil
	}
	ciphers, _, err := unstructured.NestedStringSlice(cfg, TLSCipherSuitesPath()...)
	if err != nil {
		return "", nil, nil
	}
	groups, _, err := unstructured.NestedStringSlice(cfg, TLSCurvePreferencesPath()...)
	if err != nil {
		return "", nil, nil
	}
	return minTLS, ciphers, curvePreferencesFromTLSGroups(groups)
}

// curvePreferencesFromTLSGroups converts OpenShift TLS group names to the numeric
// CurveIDs accepted by SecureServingOptions and logs groups unsupported by the
// running Go TLS implementation.
func curvePreferencesFromTLSGroups[T ~string](groups []T) []int32 {
	curvePreferences, unrecognizedGroups := crypto.TLSGroupsToCurvePreferences(groups)
	for _, group := range unrecognizedGroups {
		// This should only occur when the API adds a group before this binary's
		// Go TLS implementation can support it.
		klog.Warningf("Dropping TLS group %q: not supported by Go's crypto/tls", group)
	}
	return curvePreferences
}

// WriteMetricsServerConfigFile writes a GenericOperatorConfig JSON file containing the
// given serving info to a temp file and returns the file path. The caller is responsible
// for cleaning up the file when it is no longer needed.
func WriteMetricsServerConfigFile(servingInfo configv1.HTTPServingInfo) (string, error) {
	data, err := marshalServingConfig(servingInfo)
	if err != nil {
		return "", err
	}

	f, err := os.CreateTemp("", "cluster-olm-operator-tls-*.json")
	if err != nil {
		return "", fmt.Errorf("error creating temp TLS config file: %w", err)
	}
	defer f.Close()

	if err := f.Chmod(0600); err != nil {
		return "", fmt.Errorf("error setting TLS config file permissions: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		return "", fmt.Errorf("error writing TLS config file: %w", err)
	}

	return f.Name(), nil
}

// UpdateMetricsServerConfigFile overwrites the config file at path with new TLS serving
// info. controllercmd's WithRestartOnChange watches the config file and triggers a graceful
// restart when its content changes, so the metrics server picks up the new TLS settings.
func UpdateMetricsServerConfigFile(path string, servingInfo configv1.HTTPServingInfo) error {
	if path == "" {
		return nil
	}
	data, err := marshalServingConfig(servingInfo)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func marshalServingConfig(servingInfo configv1.HTTPServingInfo) ([]byte, error) {
	config := operatorv1alpha1.GenericOperatorConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "operator.openshift.io/v1alpha1",
			Kind:       "GenericOperatorConfig",
		},
		ServingInfo: servingInfo,
	}
	data, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("error marshaling TLS serving config: %w", err)
	}
	return data, nil
}
