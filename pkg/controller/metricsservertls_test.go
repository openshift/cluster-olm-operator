package controller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	configv1typed "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// fakeAPIServerGetter is a minimal configclient.Interface that returns a fixed APIServer.
type fakeAPIServerGetter struct {
	configclient.Interface // embed to satisfy interface; only ConfigV1().APIServers().Get is exercised
	apiServer              *configv1.APIServer
	err                    error
}

func (f *fakeAPIServerGetter) ConfigV1() configv1typed.ConfigV1Interface {
	return &fakeConfigV1Client{apiServer: f.apiServer, err: f.err}
}

type fakeConfigV1Client struct {
	configv1typed.ConfigV1Interface
	apiServer *configv1.APIServer
	err       error
}

func (f *fakeConfigV1Client) APIServers() configv1typed.APIServerInterface {
	return &fakeAPIServerClient{apiServer: f.apiServer, err: f.err}
}

type fakeAPIServerClient struct {
	configv1typed.APIServerInterface
	apiServer *configv1.APIServer
	err       error
}

func (f *fakeAPIServerClient) Get(_ context.Context, _ string, _ metav1.GetOptions) (*configv1.APIServer, error) {
	return f.apiServer, f.err
}

func TestTLSSettingsFromProfile(t *testing.T) {
	tests := []struct {
		name                string
		profile             *configv1.TLSSecurityProfile
		wantMinTLSVersion   string
		wantCipherSuitesDep bool // whether cipherSuites should be non-empty
	}{
		{
			name:                "nil profile defaults to Intermediate",
			profile:             nil,
			wantMinTLSVersion:   string(configv1.TLSProfiles[configv1.TLSProfileIntermediateType].MinTLSVersion),
			wantCipherSuitesDep: true,
		},
		{
			name:                "Intermediate profile",
			profile:             &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
			wantMinTLSVersion:   string(configv1.TLSProfiles[configv1.TLSProfileIntermediateType].MinTLSVersion),
			wantCipherSuitesDep: true,
		},
		{
			name:                "Old profile",
			profile:             &configv1.TLSSecurityProfile{Type: configv1.TLSProfileOldType},
			wantMinTLSVersion:   string(configv1.TLSProfiles[configv1.TLSProfileOldType].MinTLSVersion),
			wantCipherSuitesDep: true,
		},
		{
			name:                "Modern profile",
			profile:             &configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType},
			wantMinTLSVersion:   string(configv1.TLSProfiles[configv1.TLSProfileModernType].MinTLSVersion),
			wantCipherSuitesDep: true,
		},
		{
			name: "Custom profile",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS13,
						Ciphers:       []string{"ECDHE-ECDSA-AES256-GCM-SHA384"},
					},
				},
			},
			wantMinTLSVersion:   string(configv1.VersionTLS13),
			wantCipherSuitesDep: true,
		},
		{
			name:                "Custom profile with nil Custom spec falls back to Intermediate",
			profile:             &configv1.TLSSecurityProfile{Type: configv1.TLSProfileCustomType},
			wantMinTLSVersion:   string(configv1.TLSProfiles[configv1.TLSProfileIntermediateType].MinTLSVersion),
			wantCipherSuitesDep: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			minTLS, ciphers := tlsSettingsFromProfile(tt.profile)
			if minTLS != tt.wantMinTLSVersion {
				t.Errorf("minTLSVersion = %q, want %q", minTLS, tt.wantMinTLSVersion)
			}
			if tt.wantCipherSuitesDep && len(ciphers) == 0 {
				t.Error("expected non-empty cipherSuites")
			}
		})
	}
}

func TestGetMetricsServerTLSServingInfo_NotFound(t *testing.T) {
	client := &fakeAPIServerGetter{
		err: apierrors.NewNotFound(schema.GroupResource{Resource: "apiservers"}, "cluster"),
	}
	info, err := GetMetricsServerTLSServingInfo(context.Background(), client)
	if err != nil {
		t.Fatalf("expected no error for NotFound, got: %v", err)
	}
	if info.MinTLSVersion != "" || len(info.CipherSuites) != 0 {
		t.Error("expected empty HTTPServingInfo for NotFound")
	}
}

func TestGetMetricsServerTLSServingInfo_Error(t *testing.T) {
	client := &fakeAPIServerGetter{err: errors.New("connection refused")}
	_, err := GetMetricsServerTLSServingInfo(context.Background(), client)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetMetricsServerTLSServingInfo_IntermediateProfile(t *testing.T) {
	client := &fakeAPIServerGetter{
		apiServer: &configv1.APIServer{
			Spec: configv1.APIServerSpec{
				TLSSecurityProfile: &configv1.TLSSecurityProfile{
					Type: configv1.TLSProfileIntermediateType,
				},
			},
		},
	}
	info, err := GetMetricsServerTLSServingInfo(context.Background(), client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedMin := string(configv1.TLSProfiles[configv1.TLSProfileIntermediateType].MinTLSVersion)
	if info.MinTLSVersion != expectedMin {
		t.Errorf("MinTLSVersion = %q, want %q", info.MinTLSVersion, expectedMin)
	}
	if len(info.CipherSuites) == 0 {
		t.Error("expected non-empty CipherSuites")
	}
}

func TestWriteMetricsServerConfigFile(t *testing.T) {
	servingInfo := configv1.HTTPServingInfo{
		ServingInfo: configv1.ServingInfo{
			MinTLSVersion: "VersionTLS12",
			CipherSuites:  []string{"TLS_AES_128_GCM_SHA256", "TLS_AES_256_GCM_SHA384"},
		},
	}

	path, err := WriteMetricsServerConfigFile(servingInfo)
	if err != nil {
		t.Fatalf("WriteMetricsServerConfigFile error: %v", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("error reading written config file: %v", err)
	}

	var config operatorv1alpha1.GenericOperatorConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("error unmarshaling config file: %v", err)
	}

	if config.ServingInfo.MinTLSVersion != "VersionTLS12" {
		t.Errorf("MinTLSVersion = %q, want %q", config.ServingInfo.MinTLSVersion, "VersionTLS12")
	}
	if len(config.ServingInfo.CipherSuites) != 2 {
		t.Errorf("CipherSuites len = %d, want 2", len(config.ServingInfo.CipherSuites))
	}
	if config.Kind != "GenericOperatorConfig" {
		t.Errorf("Kind = %q, want GenericOperatorConfig", config.Kind)
	}
}
