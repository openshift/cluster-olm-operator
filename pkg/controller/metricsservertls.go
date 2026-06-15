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
)

// GetMetricsServerTLSServingInfo reads the cluster TLS security profile from the APIServer
// config and returns a configv1.HTTPServingInfo populated with MinTLSVersion and CipherSuites.
// Returns an empty HTTPServingInfo (with no error) if the APIServer resource is not found.
func GetMetricsServerTLSServingInfo(ctx context.Context, configClient configclient.Interface) (configv1.HTTPServingInfo, error) {
	apiServer, err := configClient.ConfigV1().APIServers().Get(ctx, "cluster", metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return configv1.HTTPServingInfo{}, nil
	}
	if err != nil {
		return configv1.HTTPServingInfo{}, fmt.Errorf("error reading APIServer config: %w", err)
	}

	minTLSVersion, cipherSuites := tlsSettingsFromProfile(apiServer.Spec.TLSSecurityProfile)
	return configv1.HTTPServingInfo{
		ServingInfo: configv1.ServingInfo{
			MinTLSVersion: minTLSVersion,
			CipherSuites:  cipherSuites,
		},
	}, nil
}

// tlsSettingsFromProfile extracts the minimum TLS version and IANA cipher suite names
// from a TLSSecurityProfile. Mirrors the private getSecurityProfileCiphers in library-go.
func tlsSettingsFromProfile(profile *configv1.TLSSecurityProfile) (string, []string) {
	profileType := configv1.TLSProfileIntermediateType
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
		profileSpec = configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	}

	return string(profileSpec.MinTLSVersion), crypto.OpenSSLToIANACipherSuites(profileSpec.Ciphers)
}

// TLSProfileFromObservedConfig extracts the TLS minVersion and cipherSuites stored in the
// operator's observedConfig at the olmTLSSecurityProfile paths. Returns empty strings/nil
// if the observedConfig is absent or unparseable.
func TLSProfileFromObservedConfig(operatorSpec *operatorv1.OperatorSpec) (string, []string) {
	if operatorSpec == nil || len(operatorSpec.ObservedConfig.Raw) == 0 {
		return "", nil
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(operatorSpec.ObservedConfig.Raw, &cfg); err != nil {
		return "", nil
	}
	minTLS, _, _ := unstructured.NestedString(cfg, TLSMinVersionPath()...)
	ciphers, _, _ := unstructured.NestedStringSlice(cfg, TLSCipherSuitesPath()...)
	return minTLS, ciphers
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
