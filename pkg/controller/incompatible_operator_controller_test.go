package controller

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/blang/semver/v4"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	operatorv1 "github.com/openshift/api/operator/v1"
	applyoperatorv1 "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"

	"github.com/openshift/library-go/pkg/apiserver/jsonpatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-olm-operator/pkg/clients"
)

// TestIncompatibleOperatorController_Sync tests the sync method which updates operator status conditions
// based on the presence of incompatible operators
func TestIncompatibleOperatorController_Sync(t *testing.T) {
	type args struct {
		clusterExtensions         []runtime.Object
		clusterExtensionRevisions []runtime.Object
		helmReleases              []runtime.Object
		currentOCPVersion         string
		boxCutterEnabled          bool
	}
	type wants struct {
		expectErr         bool
		conditionStatus   operatorv1.ConditionStatus
		conditionReason   string
		messageContaining string
	}
	tests := []struct {
		name  string
		args  args
		wants wants
	}{
		{
			name: "boxcutter: no operators",
			args: args{
				clusterExtensions: []runtime.Object{},
				currentOCPVersion: "4.17.0",
				boxCutterEnabled:  true,
			},
			wants: wants{
				conditionStatus: operatorv1.ConditionTrue,
				conditionReason: "",
			},
		},
		{
			name: "helm: no operators",
			args: args{
				clusterExtensions: []runtime.Object{},
				currentOCPVersion: "4.17.0",
				boxCutterEnabled:  false,
			},
			wants: wants{
				conditionStatus: operatorv1.ConditionTrue,
				conditionReason: "",
			},
		},
		{
			name: "boxcutter: compatible operator",
			args: args{
				clusterExtensions: []runtime.Object{
					createClusterExtension("test-operator"),
				},
				clusterExtensionRevisions: []runtime.Object{
					createRevision("test-operator-rev1", 1, "Active", "test-operator", "test-bundle-1.0", olmPropertyAnnotation(`[{"type":"olm.maxOpenShiftVersion","value":"4.18"}]`)),
				},
				currentOCPVersion: "4.17.0",
				boxCutterEnabled:  true,
			},
			wants: wants{
				conditionStatus: operatorv1.ConditionTrue,
				conditionReason: "",
			},
		},
		{
			name: "boxcutter: operator without olm.properties is interpreted as compatible",
			args: args{
				clusterExtensions: []runtime.Object{
					createClusterExtension("test-operator"),
				},
				clusterExtensionRevisions: []runtime.Object{
					createRevision("test-operator-rev1", 1, "Active", "test-operator", "test-bundle-1.0", nil),
				},
				currentOCPVersion: "4.17.0",
				boxCutterEnabled:  true,
			},
			wants: wants{
				conditionStatus: operatorv1.ConditionTrue,
				conditionReason: "",
			},
		},
		{
			name: "boxcutter: operator with empty olm.properties errors",
			args: args{
				clusterExtensions: []runtime.Object{
					createClusterExtension("test-operator"),
				},
				clusterExtensionRevisions: []runtime.Object{
					createRevision("test-operator-rev1", 1, "Active", "test-operator", "test-bundle-1.0", olmPropertyAnnotation("")),
				},
				currentOCPVersion: "4.17.0",
				boxCutterEnabled:  true,
			},
			wants: wants{
				expectErr:         true,
				conditionStatus:   operatorv1.ConditionFalse,
				conditionReason:   reasonFailureGettingExtension,
				messageContaining: "could not convert olm.properties",
			},
		},
		{
			name: "boxcutter: operator with non-semver olm.properties errors",
			args: args{
				clusterExtensions: []runtime.Object{
					createClusterExtension("test-operator"),
				},
				clusterExtensionRevisions: []runtime.Object{
					createRevision("test-operator-rev1", 1, "Active", "test-operator", "test-bundle-1.0", olmPropertyAnnotation("abcd")),
				},
				currentOCPVersion: "4.17.0",
				boxCutterEnabled:  true,
			},
			wants: wants{
				expectErr:         true,
				conditionStatus:   operatorv1.ConditionFalse,
				conditionReason:   reasonFailureGettingExtension,
				messageContaining: "could not convert olm.properties",
			},
		},
		{
			name: "boxcutter: incompatible operator found",
			args: args{
				clusterExtensions: []runtime.Object{
					createClusterExtension("test-operator"),
				},
				clusterExtensionRevisions: []runtime.Object{
					createRevision("test-operator-rev1", 1, "Active", "test-operator", "test-bundle-1.0", olmPropertyAnnotation(`[{"type":"olm.maxOpenShiftVersion","value":"4.17"}]`)),
				},
				currentOCPVersion: "4.17.0",
				boxCutterEnabled:  true,
			},
			wants: wants{
				conditionStatus:   operatorv1.ConditionFalse,
				conditionReason:   reasonIncompatibleOperatorsInstalled,
				messageContaining: "test-bundle-1.0",
			},
		},
		{
			name: "boxcutter: incompatible operator with multiple revisions found",
			args: args{
				clusterExtensions: []runtime.Object{
					createClusterExtension("test-operator"),
					createClusterExtension("test-operator-2"),
				},
				clusterExtensionRevisions: []runtime.Object{
					createRevision("test-operator-rev3", 3, "Active", "test-operator", "test-bundle-1.2", olmPropertyAnnotation(`[{"type":"olm.maxOpenShiftVersion","value":"4.17"}]`)),
					createRevision("test-operator-rev1", 1, "Active", "test-operator-2", "test-bundle-1.2", olmPropertyAnnotation(`[{"type":"olm.maxOpenShiftVersion","value":"4.18"}]`)),
				},
				currentOCPVersion: "4.17.0",
				boxCutterEnabled:  true,
			},
			wants: wants{
				conditionStatus:   operatorv1.ConditionFalse,
				conditionReason:   reasonIncompatibleOperatorsInstalled,
				messageContaining: "test-bundle-1.2",
			},
		},
		{
			name: "boxcutter: compatible and incompatible operators",
			args: args{
				clusterExtensions: []runtime.Object{
					createClusterExtension("test-operator"),
				},
				clusterExtensionRevisions: []runtime.Object{
					createRevision("test-operator-rev1", 1, "Archived", "test-operator", "test-bundle-1.0", olmPropertyAnnotation(`[{"type":"olm.maxOpenShiftVersion","value":"4.17"}]`)),
					// set the non-latest revision to a compatible value to ensure the latest revision value is the one being used
					createRevision("test-operator-rev2", 2, "Active", "test-operator", "test-bundle-1.1", olmPropertyAnnotation(`[{"type":"olm.maxOpenShiftVersion","value":"4.18"}]`)),
					createRevision("test-operator-rev3", 3, "Active", "test-operator", "test-bundle-1.2", olmPropertyAnnotation(`[{"type":"olm.maxOpenShiftVersion","value":"4.17"}]`)),
				},
				currentOCPVersion: "4.17.0",
				boxCutterEnabled:  true,
			},
			wants: wants{
				conditionStatus:   operatorv1.ConditionFalse,
				conditionReason:   reasonIncompatibleOperatorsInstalled,
				messageContaining: "test-bundle-1.2",
			},
		},
		{
			name: "helm: compatible operator",
			args: args{
				clusterExtensions: []runtime.Object{
					createClusterExtension("test-operator"),
				},
				helmReleases: []runtime.Object{
					createHelmReleaseSecret("test-operator", "test-bundle-1.0", "test-package", olmPropertyAnnotation(`[{"type":"olm.maxOpenShiftVersion","value":"4.18"}]`)),
				},
				currentOCPVersion: "4.17.0",
				boxCutterEnabled:  false,
			},
			wants: wants{
				conditionStatus: operatorv1.ConditionTrue,
				conditionReason: "",
			},
		},
		{
			name: "helm: operator without olm.properties is interpreted as compatible",
			args: args{
				clusterExtensions: []runtime.Object{
					createClusterExtension("test-operator"),
				},
				helmReleases: []runtime.Object{
					createHelmReleaseSecret("test-operator", "test-bundle-1.0", "test-package", nil),
				},
				currentOCPVersion: "4.17.0",
				boxCutterEnabled:  false,
			},
			wants: wants{
				conditionStatus: operatorv1.ConditionTrue,
				conditionReason: "",
			},
		},
		{
			name: "helm: incompatible operator found",
			args: args{
				clusterExtensions: []runtime.Object{
					createClusterExtension("test-operator"),
				},
				helmReleases: []runtime.Object{
					createHelmReleaseSecret("test-operator", "test-bundle-1.0", "test-package", olmPropertyAnnotation(`[{"type":"olm.maxOpenShiftVersion","value":"4.17"}]`)),
				},
				currentOCPVersion: "4.17.0",
				boxCutterEnabled:  false,
			},
			wants: wants{
				conditionStatus:   operatorv1.ConditionFalse,
				conditionReason:   reasonIncompatibleOperatorsInstalled,
				messageContaining: "test-bundle-1.0",
			},
		},
		{
			name: "helm: compatible and incompatible operators",
			args: args{
				clusterExtensions: []runtime.Object{
					createClusterExtension("test-operator"),
					createClusterExtension("test-operator-2"),
				},
				helmReleases: []runtime.Object{
					createHelmReleaseSecret("test-operator", "test-bundle-1.0", "test-package", olmPropertyAnnotation(`[{"type":"olm.maxOpenShiftVersion","value":"4.17"}]`)),
					createHelmReleaseSecret("test-operator-2", "test-bundle-2.0", "test-package-2", olmPropertyAnnotation(`[{"type":"olm.maxOpenShiftVersion","value":"4.18"}]`)),
				},
				currentOCPVersion: "4.17.0",
				boxCutterEnabled:  false,
			},
			wants: wants{
				conditionStatus:   operatorv1.ConditionFalse,
				conditionReason:   reasonIncompatibleOperatorsInstalled,
				messageContaining: "test-bundle-1.0",
			},
		},
		{
			name: "helm: operator with empty olm.properties errors",
			args: args{
				clusterExtensions: []runtime.Object{
					createClusterExtension("test-operator"),
				},
				helmReleases: []runtime.Object{
					createHelmReleaseSecret("test-operator", "test-bundle-1.0", "test-package", olmPropertyAnnotation("")),
				},
				currentOCPVersion: "4.17.0",
				boxCutterEnabled:  false,
			},
			wants: wants{
				expectErr:         true,
				conditionStatus:   operatorv1.ConditionFalse,
				conditionReason:   reasonFailureGettingExtension,
				messageContaining: "could not convert olm.properties",
			},
		},
		{
			name: "helm: operator with invalid olm.properties errors",
			args: args{
				clusterExtensions: []runtime.Object{
					createClusterExtension("test-operator"),
				},
				helmReleases: []runtime.Object{
					createHelmReleaseSecret("test-operator", "test-bundle-1.0", "test-package", olmPropertyAnnotation("abcd")),
				},
				currentOCPVersion: "4.17.0",
				boxCutterEnabled:  false,
			},
			wants: wants{
				expectErr:         true,
				conditionStatus:   operatorv1.ConditionFalse,
				conditionReason:   reasonFailureGettingExtension,
				messageContaining: "could not convert olm.properties",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version := semver.MustParse(tt.args.currentOCPVersion)

			// Create OLM resource that the operator client will manage
			olmResource := &operatorv1.OLM{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: operatorv1.OLMSpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: operatorv1.Managed,
					},
				},
				Status: operatorv1.OLMStatus{},
			}

			// Setup operator client
			operatorClient := newMockOperatorClient(olmResource)

			// Setup dynamic client with scheme
			scheme := runtime.NewScheme()
			_ = operatorv1.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)

			allObjects := slices.Concat(tt.args.clusterExtensions, tt.args.clusterExtensionRevisions)
			dynClient := dynamicfake.NewSimpleDynamicClient(scheme, allObjects...)

			clusterExtensionClient := clients.NewClusterExtensionClient(dynClient)
			clusterExtensionRevisionClient := clients.NewClusterExtensionRevisionClient(dynClient)

			// Setup kube client with Helm releases
			kubeClient := fake.NewClientset(tt.args.helmReleases...)

			// Setup feature gate
			enabledGates := []configv1.FeatureGateName{}
			if tt.args.boxCutterEnabled {
				enabledGates = append(enabledGates, features.FeatureGateNewOLMBoxCutterRuntime)
			}
			featureGate := newMockFeatureGate(enabledGates, []configv1.FeatureGateName{})

			// Create controller
			controller := &incompatibleOperatorController{
				name:                           "test-controller",
				currentOCPMinorVersion:         &version,
				kubeclient:                     kubeClient,
				clusterExtensionClient:         clusterExtensionClient,
				clusterExtensionRevisionClient: clusterExtensionRevisionClient,
				operatorClient:                 operatorClient,
				featureGate:                    featureGate,
				logger:                         klog.NewKlogr(),
			}

			// Add objects to informer cache
			for _, ce := range tt.args.clusterExtensions {
				_ = clusterExtensionClient.Informer().Informer().GetIndexer().Add(ce)
			}
			for _, rev := range tt.args.clusterExtensionRevisions {
				_ = clusterExtensionRevisionClient.Informer().Informer().GetIndexer().Add(rev)
			}

			// Call sync
			err := controller.sync(context.Background(), nil)
			if tt.wants.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			// Get updated OLM status from the mock operator client
			_, status, _, err := operatorClient.GetOperatorState()
			require.NoError(t, err)

			// Verify condition was set correctly
			var condition *operatorv1.OperatorCondition
			for i := range status.Conditions {
				if status.Conditions[i].Type == typeIncompatibelOperatorsUpgradeable {
					condition = &status.Conditions[i]
					break
				}
			}

			require.NotNil(t, condition, "Expected condition %s to be set", typeIncompatibelOperatorsUpgradeable)
			assert.Equal(t, tt.wants.conditionStatus, condition.Status, "Condition status mismatch")

			if tt.wants.conditionReason != "" {
				assert.Equal(t, tt.wants.conditionReason, condition.Reason, "Condition reason mismatch")
			}

			if tt.wants.messageContaining != "" {
				assert.Contains(t, condition.Message, tt.wants.messageContaining, "Condition message should contain expected text")
			}
		})
	}
}

func createRevision(name string, revision int64, lifecycleState string, ownerName string, bundleName string, annotations map[string]string) *unstructured.Unstructured {
	revAnnotations := map[string]interface{}{
		bundleNameKey: bundleName,
	}
	for k, v := range annotations {
		revAnnotations[k] = v
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "olm.operatorframework.io/v1",
			"kind":       "ClusterExtensionRevision",
			"metadata": map[string]interface{}{
				"name":        name,
				"annotations": revAnnotations,
				"labels": map[string]interface{}{
					ownerKindKey: "ClusterExtension",
					ownerNameKey: ownerName,
				},
			},
			"spec": map[string]interface{}{
				"revision":       revision,
				"lifecycleState": lifecycleState,
			},
		},
	}
}

func createClusterExtension(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "olm.operatorframework.io/v1",
			"kind":       "ClusterExtension",
			"metadata": map[string]interface{}{
				"name": name,
			},
		},
	}
}

func olmPropertyAnnotation(value string) map[string]string {
	return map[string]string{
		olmPropertiesKey: value,
	}
}

// Helper to create Helm release secret with properties in chart metadata
func createHelmReleaseSecret(name, bundleName, packageName string, annotations map[string]string) *corev1.Secret {
	// Helm v3 driver expects these standard labels
	// The chunked secrets driver specifically requires "type": "index" for index secrets
	helmLabels := map[string]string{
		"name":    name,
		"owner":   "operator-controller",
		"status":  "deployed",
		"version": "1",
		"type":    "index", // Required by chunked secrets driver
	}

	// OLM-specific labels for the release
	olmLabels := map[string]string{
		ownerKindKey:   "helm",
		ownerNameKey:   name,
		packageNameKey: packageName,
		bundleNameKey:  bundleName,
	}

	// Combine labels
	allLabels := make(map[string]string)
	for k, v := range helmLabels {
		allLabels[k] = v
	}
	for k, v := range olmLabels {
		allLabels[k] = v
	}

	// Create a minimal Helm v3 release structure that matches the actual release.Release type
	releaseData := map[string]interface{}{
		"name":      name,
		"namespace": "openshift-operator-controller",
		"version":   1,
		"info": map[string]interface{}{
			"status":      "deployed",
			"description": "test release",
		},
		"chart": map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":        bundleName,
				"version":     "1.0.0",
				"apiVersion":  "v2",
				"annotations": annotations,
			},
		},
		"config":   map[string]interface{}{},
		"manifest": "",
		"labels":   olmLabels, // Release wrapper labels (OLM-specific)
	}

	releaseDataJSON, _ := json.Marshal(releaseData)

	// Chunked secrets driver expects gzipped data in "chunk" key
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	_, _ = gzipWriter.Write(releaseDataJSON)
	_ = gzipWriter.Close()

	// For single-chunk releases, extraChunks is an empty array
	extraChunks, _ := json.Marshal([]string{})

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("sh.helm.release.v1.%s.v1", name),
			Namespace: "openshift-operator-controller",
			Labels:    allLabels, // Secret has both Helm and OLM labels
		},
		Type: "helm.sh/release.v1",
		Data: map[string][]byte{
			"chunk":       buf.Bytes(), // Gzipped release data
			"extraChunks": extraChunks, // Empty array for single-chunk release
		},
	}
}

// mockOperatorClient is a simple in-memory implementation of v1helpers.OperatorClient for testing
type mockOperatorClient struct {
	mu              sync.Mutex
	olm             *operatorv1.OLM
	resourceVersion int
	informer        cache.SharedIndexInformer
}

func newMockOperatorClient(olmResource *operatorv1.OLM) *mockOperatorClient {
	return &mockOperatorClient{
		olm:             olmResource.DeepCopy(),
		resourceVersion: 1,
		informer:        nil, // Not needed for these tests
	}
}

func (m *mockOperatorClient) Informer() cache.SharedIndexInformer {
	return m.informer
}

func (m *mockOperatorClient) GetObjectMeta() (*metav1.ObjectMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return &m.olm.ObjectMeta, nil
}

func (m *mockOperatorClient) GetOperatorState() (*operatorv1.OperatorSpec, *operatorv1.OperatorStatus, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rv := fmt.Sprintf("%d", m.resourceVersion)
	return &m.olm.Spec.OperatorSpec, &m.olm.Status.OperatorStatus, rv, nil
}

func (m *mockOperatorClient) GetOperatorStateWithQuorum(_ context.Context) (*operatorv1.OperatorSpec, *operatorv1.OperatorStatus, string, error) {
	return m.GetOperatorState()
}

func (m *mockOperatorClient) UpdateOperatorSpec(_ context.Context, _ string, in *operatorv1.OperatorSpec) (*operatorv1.OperatorSpec, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.olm.Spec.OperatorSpec = *in
	m.resourceVersion++
	rv := fmt.Sprintf("%d", m.resourceVersion)
	return &m.olm.Spec.OperatorSpec, rv, nil
}

func (m *mockOperatorClient) UpdateOperatorStatus(_ context.Context, _ string, in *operatorv1.OperatorStatus) (*operatorv1.OperatorStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.olm.Status.OperatorStatus = *in
	m.resourceVersion++
	return &m.olm.Status.OperatorStatus, nil
}

func (m *mockOperatorClient) ApplyOperatorSpec(_ context.Context, _ string, _ *applyoperatorv1.OperatorSpecApplyConfiguration) error {
	// Not needed for these tests
	return nil
}

func (m *mockOperatorClient) ApplyOperatorStatus(_ context.Context, _ string, _ *applyoperatorv1.OperatorStatusApplyConfiguration) error {
	// Not needed for these tests
	return nil
}

func (m *mockOperatorClient) PatchOperatorStatus(_ context.Context, _ *jsonpatch.PatchSet) error {
	// Not needed for these tests
	return nil
}
