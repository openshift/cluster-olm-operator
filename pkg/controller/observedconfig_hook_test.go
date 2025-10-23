package controller

import (
	"testing"

	operatorv1 "github.com/openshift/api/operator/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestUpdateDeploymentObservedConfigHook(t *testing.T) {
	tests := []struct {
		name         string
		operatorSpec *operatorv1.OperatorSpec
		expectedArgs []string
		expectError  bool
	}{
		{
			name: "valid TLS configuration",
			operatorSpec: &operatorv1.OperatorSpec{
				ObservedConfig: runtime.RawExtension{
					Raw: []byte(`{
						"olmTLSSecurityProfile": {
							"minTLSVersion": "VersionTLS12",
							"cipherSuites": ["TLS_AES_128_GCM_SHA256", "TLS_AES_256_GCM_SHA384"]
						}
					}`),
				},
			},
			expectedArgs: []string{
				"--tls-custom-version=TLSv1.2",
				"--tls-custom-ciphers=TLS_AES_128_GCM_SHA256,TLS_AES_256_GCM_SHA384",
				"--tls-profile=custom",
			},
			expectError: false,
		},
		{
			name: "empty observedConfig",
			operatorSpec: &operatorv1.OperatorSpec{
				ObservedConfig: runtime.RawExtension{},
			},
			expectedArgs: []string{},
			expectError:  false,
		},
		{
			name: "no TLS profile in observedConfig",
			operatorSpec: &operatorv1.OperatorSpec{
				ObservedConfig: runtime.RawExtension{
					Raw: []byte(`{"someOtherConfig": "value"}`),
				},
			},
			expectedArgs: []string{},
			expectError:  false,
		},
		{
			name: "only minTLSVersion",
			operatorSpec: &operatorv1.OperatorSpec{
				ObservedConfig: runtime.RawExtension{
					Raw: []byte(`{
						"olmTLSSecurityProfile": {
							"minTLSVersion": "VersionTLS13"
						}
					}`),
				},
			},
			expectedArgs: nil,
			expectError:  true,
		},
		{
			name: "only cipherSuites",
			operatorSpec: &operatorv1.OperatorSpec{
				ObservedConfig: runtime.RawExtension{
					Raw: []byte(`{
						"olmTLSSecurityProfile": {
							"cipherSuites": ["TLS_AES_128_GCM_SHA256"]
						}
					}`),
				},
			},
			expectedArgs: nil,
			expectError:  true,
		},
		{
			name: "TLS version translation",
			operatorSpec: &operatorv1.OperatorSpec{
				ObservedConfig: runtime.RawExtension{
					Raw: []byte(`{
						"olmTLSSecurityProfile": {
							"minTLSVersion": "VersionTLS11"
						}
					}`),
				},
			},
			expectedArgs: nil,
			expectError:  true,
		},
		{
			name: "valid complete TLS configuration with custom profile",
			operatorSpec: &operatorv1.OperatorSpec{
				ObservedConfig: runtime.RawExtension{
					Raw: []byte(`{
						"olmTLSSecurityProfile": {
							"minTLSVersion": "VersionTLS11",
							"cipherSuites": ["TLS_AES_128_GCM_SHA256", "TLS_AES_256_GCM_SHA384"]
						}
					}`),
				},
			},
			expectedArgs: []string{
				"--tls-custom-version=TLSv1.1",
				"--tls-custom-ciphers=TLS_AES_128_GCM_SHA256,TLS_AES_256_GCM_SHA384",
				"--tls-profile=custom",
			},
			expectError: false,
		},
		{
			name:         "nil operatorSpec",
			operatorSpec: nil,
			expectedArgs: []string{},
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the hook function directly using the extractTLSConfigFromObservedConfig function
			args, err := extractTLSConfigFromObservedConfig(tt.operatorSpec)
			if tt.expectError && err == nil {
				t.Fatal("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Verify the arguments
			if len(args) != len(tt.expectedArgs) {
				t.Fatalf("expected %d args, got %d", len(tt.expectedArgs), len(args))
			}

			for i, expected := range tt.expectedArgs {
				if args[i] != expected {
					t.Errorf("arg %d: expected %q, got %q", i, expected, args[i])
				}
			}
		})
	}
}

func TestExtractTLSConfigFromObservedConfig(t *testing.T) {
	tests := []struct {
		name           string
		observedConfig string
		expectedArgs   []string
		expectError    bool
	}{
		{
			name: "invalid JSON",
			observedConfig: `{
				"olmTLSSecurityProfile": {
					"minTLSVersion": "VersionTLS12",
					"cipherSuites": [
				}
			}`,
			expectedArgs: nil,
			expectError:  true,
		},
		{
			name: "empty TLS profile",
			observedConfig: `{
				"olmTLSSecurityProfile": {}
			}`,
			expectedArgs: []string{},
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			operatorSpec := &operatorv1.OperatorSpec{
				ObservedConfig: runtime.RawExtension{
					Raw: []byte(tt.observedConfig),
				},
			}

			args, err := extractTLSConfigFromObservedConfig(operatorSpec)
			if tt.expectError && err == nil {
				t.Fatal("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !tt.expectError {
				if len(args) != len(tt.expectedArgs) {
					t.Fatalf("expected %d args, got %d", len(tt.expectedArgs), len(args))
				}

				for i, expected := range tt.expectedArgs {
					if args[i] != expected {
						t.Errorf("arg %d: expected %q, got %q", i, expected, args[i])
					}
				}
			}
		})
	}
}
