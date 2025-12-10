// Package controller provides deployment hooks for configuring TLS security profiles
// and proxy settings from OpenShift cluster configuration.
package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/deploymentcontroller"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-olm-operator/pkg/clients"
)

// UpdateDeploymentObservedConfigHook creates a deployment hook that reads observedConfig
// from the olms.operator.openshift.io resource and extracts TLS configuration
func UpdateDeploymentObservedConfigHook(_ *clients.OperatorClient) deploymentcontroller.DeploymentHookFunc {
	return func(operatorSpec *operatorv1.OperatorSpec, deployment *appsv1.Deployment) error {
		klog.V(1).Infof("ObservedConfigHook updating arguments for deployment %s", deployment.Name)

		// Extract TLS configuration from observedConfig
		cfg, err := extractTLSConfigFromObservedConfig(operatorSpec)
		if err != nil {
			klog.V(2).Infof("Failed to extract TLS config from observedConfig: %v", err)
			// Don't return error - just log and continue without TLS env vars
			return nil
		}

		if len(cfg) == 0 {
			klog.V(2).Info("No TLS configuration found in observedConfig")
			return nil
		}

		klog.V(2).Infof("Found %d TLS environment variables from observedConfig", len(cfg))

		var errs []error
		for i := range deployment.Spec.Template.Spec.Containers {
			err = setContainerArgs(&deployment.Spec.Template.Spec.Containers[i], cfg)
			if err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) > 0 {
			return errors.Join(errs...)
		}

		return nil
	}
}

// extractTLSConfigFromObservedConfig extracts TLS security profile configuration from
// the operator's observedConfig and returns environment variables
func extractTLSConfigFromObservedConfig(operatorSpec *operatorv1.OperatorSpec) ([]string, error) {
	if operatorSpec == nil || len(operatorSpec.ObservedConfig.Raw) == 0 {
		return nil, nil
	}

	// Parse the observedConfig JSON
	var observedConfig map[string]interface{}
	if err := json.Unmarshal(operatorSpec.ObservedConfig.Raw, &observedConfig); err != nil {
		return nil, fmt.Errorf("error unmarshaling observedConfig: %w", err)
	}

	// Extract TLS security profile configuration
	tlsProfile, found, err := unstructured.NestedMap(observedConfig, "olmTLSSecurityProfile")
	if err != nil {
		return nil, fmt.Errorf("error accessing olmTLSSecurityProfile: %w", err)
	}
	if !found {
		klog.V(3).Info("No olmTLSSecurityProfile found in observedConfig")
		return nil, nil
	}

	var args []string

	// Extract minTLSVersion
	if minTLSVersion, found, err := unstructured.NestedString(tlsProfile, "minTLSVersion"); err != nil {
		return nil, fmt.Errorf("error accessing minTLSVersion: %w", err)
	} else if found && minTLSVersion != "" {
		translateTLSVersions := map[string]string{
			"VersionTLS1":  "TLSv1.0",
			"VersionTLS10": "TLSv1.0",
			"VersionTLS11": "TLSv1.1",
			"VersionTLS12": "TLSv1.2",
			"VersionTLS13": "TLSv1.3",
		}
		argTLSVersion, ok := translateTLSVersions[minTLSVersion]
		if !ok {
			return nil, fmt.Errorf("unknown TLS version: %q", minTLSVersion)
		}
		args = append(args, fmt.Sprintf("--tls-custom-version=%s", argTLSVersion))
		klog.V(3).Infof("Extracted minTLSVersion: %s", argTLSVersion)
	}

	// Extract cipherSuites
	if cipherSuites, found, err := unstructured.NestedStringSlice(tlsProfile, "cipherSuites"); err != nil {
		return nil, fmt.Errorf("error accessing cipherSuites: %w", err)
	} else if found && len(cipherSuites) > 0 {
		// Join cipher suites with commas for environment variable
		cipherSuitesStr := strings.Join(cipherSuites, ",")
		args = append(args, fmt.Sprintf("--tls-custom-ciphers=%s", cipherSuitesStr))
		klog.V(3).Infof("Extracted %d cipher suites: %s", len(cipherSuites), cipherSuitesStr)
	}

	switch len(args) {
	case 0:
	case 2:
		args = append(args, "--tls-profile=custom")
	default:
		return nil, fmt.Errorf("invalid observedConfig for TLS, missing argument")
	}

	return args, nil
}

func updateArgs(con *corev1.Container, arg string) error {
	splitStrings := strings.Split(arg, "=")
	prefix := splitStrings[0]
	for _, e := range con.Args {
		if strings.HasPrefix(e, prefix) {
			return fmt.Errorf("unexpected argument %q in container %q while building manifests", arg, con.Name)
		}
	}
	klog.V(4).Infof("Updated arguments for container %s: %s", con.Name, arg)
	con.Args = append(con.Args, arg)
	return nil
}

func setContainerArgs(con *corev1.Container, args []string) error {
	for _, arg := range args {
		if err := updateArgs(con, arg); err != nil {
			return err
		}
	}
	return nil
}
