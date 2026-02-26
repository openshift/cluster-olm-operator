package clients

import (
	"context"
	"errors"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/clock"
)

// fakeClusterOperatorClient implements minimal interface for testing
type fakeClusterOperatorClient struct {
	configv1client.ClusterOperatorInterface
	co *configv1.ClusterOperator
}

func (f *fakeClusterOperatorClient) UpdateStatus(_ context.Context, co *configv1.ClusterOperator, _ metav1.UpdateOptions) (*configv1.ClusterOperator, error) {
	return co, nil
}

// fakeClusterOperatorLister implements a simple lister for testing
type fakeClusterOperatorLister struct {
	co  *configv1.ClusterOperator
	err error
}

func (f *fakeClusterOperatorLister) List(_ labels.Selector) ([]*configv1.ClusterOperator, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []*configv1.ClusterOperator{f.co}, nil
}

func (f *fakeClusterOperatorLister) Get(_ string) (*configv1.ClusterOperator, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.co, nil
}

type fakeConfigClient struct {
	configv1client.ConfigV1Interface
	coInterface configv1client.ClusterOperatorInterface
}

func (f *fakeConfigClient) ClusterOperators() configv1client.ClusterOperatorInterface {
	return f.coInterface
}

func TestConfigClientWrapperUpdateStatus(t *testing.T) {
	tests := []struct {
		name              string
		existingVersions  []configv1.OperandVersion
		releaseVersion    string
		expectProgressing bool
		expectError       bool
		notFoundError     bool
	}{
		{
			name:              "version matches - no injection",
			existingVersions:  []configv1.OperandVersion{{Name: "operator", Version: "4.22.0"}},
			releaseVersion:    "4.22.0",
			expectProgressing: false,
		},
		{
			name:              "version mismatch - inject progressing",
			existingVersions:  []configv1.OperandVersion{{Name: "operator", Version: "4.21.0"}},
			releaseVersion:    "4.22.0",
			expectProgressing: true,
		},
		{
			name:              "initial install - no injection of progressing",
			existingVersions:  []configv1.OperandVersion{},
			releaseVersion:    "4.22.0",
			expectProgressing: false,
		},
		{
			name:              "empty release version - pass through",
			existingVersions:  []configv1.OperandVersion{{Name: "operator", Version: "4.22.0"}},
			releaseVersion:    "",
			expectProgressing: false,
		},
		{
			name:             "lister error - should return error",
			existingVersions: nil,
			releaseVersion:   "4.22.0",
			expectError:      true,
		},
		{
			name:              "not found error - should proceed with update",
			existingVersions:  nil,
			releaseVersion:    "4.22.0",
			expectProgressing: false,
			notFoundError:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing := &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "olm"},
				Status:     configv1.ClusterOperatorStatus{Versions: tt.existingVersions},
			}

			var listerErr error
			if tt.expectError {
				listerErr = errors.New("lister error")
			} else if tt.notFoundError {
				listerErr = apierrors.NewNotFound(schema.GroupResource{Group: "config.openshift.io", Resource: "clusteroperators"}, "olm")
			}
			lister := &fakeClusterOperatorLister{co: existing, err: listerErr}

			wrapper := NewConfigClientWrapper(
				&fakeConfigClient{coInterface: &fakeClusterOperatorClient{co: existing}},
				lister,
				tt.releaseVersion,
				clock.RealClock{},
			)

			input := &configv1.ClusterOperator{ObjectMeta: metav1.ObjectMeta{Name: "olm"}}
			result, err := wrapper.ClusterOperators().UpdateStatus(context.Background(), input, metav1.UpdateOptions{})

			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Check if Progressing with UpgradeInProgress was injected
			foundProgressing := false
			for _, cond := range result.Status.Conditions {
				if cond.Type == configv1.OperatorProgressing && cond.Reason == "UpgradeInProgress" {
					foundProgressing = true
					if cond.Status != configv1.ConditionTrue {
						t.Errorf("expected Progressing=True, got %v", cond.Status)
					}
				}
			}

			if foundProgressing != tt.expectProgressing {
				t.Errorf("expectProgressing=%v but foundProgressing=%v", tt.expectProgressing, foundProgressing)
			}
		})
	}
}
