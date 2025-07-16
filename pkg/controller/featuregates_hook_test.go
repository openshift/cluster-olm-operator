package controller_test

import (
	"context"
	"errors"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/cluster-olm-operator/pkg/controller"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	operatorControllerDeploymentName = "operator-controller-controller-manager"
	catalogdDeploymentName           = "catalogd-controller-manager"
)

type MockFeatureGateAccessor struct {
	featureGate featuregates.FeatureGate
	err         error
}

func (ma *MockFeatureGateAccessor) SetChangeHandler(_ featuregates.FeatureGateChangeHandlerFunc) {}
func (ma *MockFeatureGateAccessor) Run(_ context.Context)                                        {}
func (ma *MockFeatureGateAccessor) InitialFeatureGatesObserved() <-chan struct{}                 { return nil }
func (ma *MockFeatureGateAccessor) AreInitialFeatureGatesObserved() bool                         { return false }
func (ma *MockFeatureGateAccessor) CurrentFeatureGates() (featuregates.FeatureGate, error) {
	return ma.featureGate, ma.err
}

type MockFeatureGateMapper struct {
	ctrlOut               []string
	ctrlDownstreamKeys    []configv1.FeatureGateName
	ctrlUpForDownCalls    int
	ctrlFeatureGatesCalls int

	catalogOut               []string
	catalogDownstreamKeys    []configv1.FeatureGateName
	catalogUpForDownCalls    int
	catalogFeatureGatesCalls int
}

func (mm *MockFeatureGateMapper) OperatorControllerUpstreamForDownstream(_ configv1.FeatureGateName) []string {
	mm.ctrlUpForDownCalls++
	return mm.ctrlOut
}
func (mm *MockFeatureGateMapper) OperatorControllerDownstreamFeatureGates() []configv1.FeatureGateName {
	mm.ctrlFeatureGatesCalls++
	return mm.ctrlDownstreamKeys
}
func (mm *MockFeatureGateMapper) CatalogdUpstreamForDownstream(_ configv1.FeatureGateName) []string {
	mm.catalogUpForDownCalls++
	return mm.catalogOut
}
func (mm *MockFeatureGateMapper) CatalogdDownstreamFeatureGates() []configv1.FeatureGateName {
	mm.catalogFeatureGatesCalls++
	return mm.catalogDownstreamKeys
}

func (mm *MockFeatureGateMapper) ValidateCalls(t *testing.T, ctrlUpDown, ctrlList, catalogUpDown, catalogList int) {
	if ctrlUpDown != mm.ctrlUpForDownCalls {
		t.Fatalf("expected %d calls to ControllerUpstreamForDownstream, got %d", mm.ctrlUpForDownCalls, ctrlUpDown)
	}
	if ctrlList != mm.ctrlFeatureGatesCalls {
		t.Fatalf("expected %d calls to ControllerDownstreamFeatureGates, got %d", mm.ctrlFeatureGatesCalls, ctrlList)
	}
	if catalogUpDown != mm.catalogUpForDownCalls {
		t.Fatalf("expected %d calls to CatalogdUpstreamForDownstream, got %d", mm.catalogUpForDownCalls, catalogUpDown)
	}
	if catalogList != mm.catalogFeatureGatesCalls {
		t.Fatalf("expected %d calls to CatalogdDownstreamFeatureGates, got %d", mm.ctrlFeatureGatesCalls, catalogList)
	}
}

func TestUpdateDeploymentFeatureGatesHook(t *testing.T) {
	testDep := appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test",
						},
					},
				},
			},
		},
	}

	t.Run("Fails because of FeatureGateAccessor error", func(t *testing.T) {
		dep := testDep.DeepCopy()

		mockAccessor := &MockFeatureGateAccessor{
			featureGate: featuregates.NewFeatureGate(
				[]configv1.FeatureGateName{}, []configv1.FeatureGateName{features.FeatureGateNewOLM},
			),
			err: errors.New("fail"),
		}
		mockMapper := &MockFeatureGateMapper{}

		update := controller.UpdateDeploymentFeatureGatesHook(mockAccessor, mockMapper)
		err := update(nil, dep)
		if err == nil {
			t.Fatalf("expected error but got nil")
		}
		mockMapper.ValidateCalls(t, 0, 0, 0, 0)
	})

	t.Run("unrecognized deployment name - no-op", func(t *testing.T) {
		dep := testDep.DeepCopy()
		dep.Name = "unrecognized"

		mockAccessor := &MockFeatureGateAccessor{
			featureGate: featuregates.NewFeatureGate(
				[]configv1.FeatureGateName{features.FeatureGateNewOLM}, []configv1.FeatureGateName{},
			),
			err: nil,
		}
		mockMapper := &MockFeatureGateMapper{}

		update := controller.UpdateDeploymentFeatureGatesHook(mockAccessor, mockMapper)
		err := update(nil, dep)
		if err != nil {
			t.Fatalf("unexpected error in first update: %v", err)
		}
		if len(dep.Spec.Template.Spec.Containers[0].Args) != 0 {
			t.Fatalf("args length not 0: %+v", dep)
		}
		mockMapper.ValidateCalls(t, 0, 0, 0, 0)
	})

	t.Run("controller mapping exists, but no enabled features - no-op", func(t *testing.T) {
		dep := testDep.DeepCopy()
		dep.Name = operatorControllerDeploymentName

		mockAccessor := &MockFeatureGateAccessor{
			featureGate: featuregates.NewFeatureGate(
				[]configv1.FeatureGateName{}, []configv1.FeatureGateName{features.FeatureGateNewOLM},
			),
			err: nil,
		}
		mockMapper := &MockFeatureGateMapper{ctrlDownstreamKeys: []configv1.FeatureGateName{features.FeatureGateNewOLM}}

		update := controller.UpdateDeploymentFeatureGatesHook(mockAccessor, mockMapper)
		err := update(nil, dep)
		if err != nil {
			t.Fatalf("unexpected error in first update: %v", err)
		}
		if len(dep.Spec.Template.Spec.Containers[0].Args) != 0 {
			t.Fatalf("args length not 0: %+v", dep)
		}
		mockMapper.ValidateCalls(t, 0, 1, 0, 0)
	})

	t.Run("catalogd mapping exists, but no enabled features - no-op", func(t *testing.T) {
		dep := testDep.DeepCopy()
		dep.Name = catalogdDeploymentName

		mockAccessor := &MockFeatureGateAccessor{
			featureGate: featuregates.NewFeatureGate(
				[]configv1.FeatureGateName{}, []configv1.FeatureGateName{features.FeatureGateNewOLM},
			),
			err: nil,
		}
		mockMapper := &MockFeatureGateMapper{catalogDownstreamKeys: []configv1.FeatureGateName{features.FeatureGateNewOLM}}

		update := controller.UpdateDeploymentFeatureGatesHook(mockAccessor, mockMapper)
		err := update(nil, dep)
		if err != nil {
			t.Fatalf("unexpected error in first update: %v", err)
		}
		if len(dep.Spec.Template.Spec.Containers[0].Args) != 0 {
			t.Fatalf("args length not 0: %+v", dep)
		}
		mockMapper.ValidateCalls(t, 0, 0, 0, 1)
	})

	t.Run("controller mapping exists with enabled features, but no matching containers - no-op", func(t *testing.T) {
		dep := testDep.DeepCopy()
		dep.Name = operatorControllerDeploymentName

		enabledFeatures := []configv1.FeatureGateName{features.FeatureGateNewOLM}
		mockAccessor := &MockFeatureGateAccessor{
			featureGate: featuregates.NewFeatureGate(
				enabledFeatures, []configv1.FeatureGateName{},
			),
			err: nil,
		}
		mockMapper := &MockFeatureGateMapper{ctrlDownstreamKeys: enabledFeatures}

		update := controller.UpdateDeploymentFeatureGatesHook(mockAccessor, mockMapper)
		err := update(nil, dep)
		if err != nil {
			t.Fatalf("unexpected error in first update: %v", err)
		}
		if len(dep.Spec.Template.Spec.Containers[0].Args) != 0 {
			t.Fatalf("args length not 0: %+v", dep)
		}
		mockMapper.ValidateCalls(t, 1, 1, 0, 0)
	})

	t.Run("catalog mapping exists with enabled features, but no matching containers - no-op", func(t *testing.T) {
		dep := testDep.DeepCopy()
		dep.Name = catalogdDeploymentName

		enabledFeatures := []configv1.FeatureGateName{features.FeatureGateNewOLM}
		mockAccessor := &MockFeatureGateAccessor{
			featureGate: featuregates.NewFeatureGate(
				enabledFeatures, []configv1.FeatureGateName{},
			),
			err: nil,
		}
		mockMapper := &MockFeatureGateMapper{catalogDownstreamKeys: enabledFeatures}

		update := controller.UpdateDeploymentFeatureGatesHook(mockAccessor, mockMapper)
		err := update(nil, dep)
		if err != nil {
			t.Fatalf("unexpected error in first update: %v", err)
		}
		if len(dep.Spec.Template.Spec.Containers[0].Args) != 0 {
			t.Fatalf("args length not 0: %+v", dep)
		}
		mockMapper.ValidateCalls(t, 0, 0, 1, 1)
	})

	t.Run("controller mapping exists with some enabled features and matching container", func(t *testing.T) {
		dep := testDep.DeepCopy()
		dep.Name = operatorControllerDeploymentName
		dep.Spec.Template.Spec.Containers[0].Name = "manager"
		dep.Spec.Template.Spec.Containers[0].Args = []string{"--feature-gates=TestUpstreamGate2=true", "--feature-gates=TestUpstreamGate1=true"}

		enabledFeatures := []configv1.FeatureGateName{features.FeatureGateNewOLM, features.FeatureGateExample}
		mockAccessor := &MockFeatureGateAccessor{
			featureGate: featuregates.NewFeatureGate(
				enabledFeatures, []configv1.FeatureGateName{},
			),
			err: nil,
		}
		mockMapper := &MockFeatureGateMapper{
			ctrlDownstreamKeys: []configv1.FeatureGateName{features.FeatureGateExample},
			ctrlOut:            []string{"TestUpstreamGate2", "TestUpstreamGate1"},
		}

		update := controller.UpdateDeploymentFeatureGatesHook(mockAccessor, mockMapper)
		err := update(nil, dep)
		if err != nil {
			t.Fatalf("unexpected error in first update: %v", err)
		}
		if len(dep.Spec.Template.Spec.Containers[0].Args) != 1 {
			t.Fatalf("args length not 1: %+v", dep)
		}
		expectedArg := "--feature-gates=TestUpstreamGate1=true,TestUpstreamGate2=true"
		if expectedArg != dep.Spec.Template.Spec.Containers[0].Args[0] {
			t.Fatalf("args differ, container: %q, expected: %q", dep.Spec.Template.Spec.Containers[0].Args[0], expectedArg)
		}
		mockMapper.ValidateCalls(t, 1, 1, 0, 0)
	})

	t.Run("catalog mapping exists with some enabled features and matching container", func(t *testing.T) {
		dep := testDep.DeepCopy()
		dep.Name = catalogdDeploymentName
		dep.Spec.Template.Spec.Containers[0].Name = "manager"
		dep.Spec.Template.Spec.Containers[0].Args = []string{"--feature-gates=TestUpstreamGate2=true", "--feature-gates=TestUpstreamGate1=true"}

		enabledFeatures := []configv1.FeatureGateName{features.FeatureGateNewOLM, features.FeatureGateExample}
		mockAccessor := &MockFeatureGateAccessor{
			featureGate: featuregates.NewFeatureGate(
				enabledFeatures, []configv1.FeatureGateName{},
			),
			err: nil,
		}
		mockMapper := &MockFeatureGateMapper{
			catalogDownstreamKeys: []configv1.FeatureGateName{features.FeatureGateExample},
			catalogOut:            []string{"TestUpstreamGate2", "TestUpstreamGate1"},
		}

		update := controller.UpdateDeploymentFeatureGatesHook(mockAccessor, mockMapper)
		err := update(nil, dep)
		if err != nil {
			t.Fatalf("unexpected error in first update: %v", err)
		}
		if len(dep.Spec.Template.Spec.Containers[0].Args) != 1 {
			t.Fatalf("args length not 1: %+v", dep)
		}
		expectedArg := "--feature-gates=TestUpstreamGate1=true,TestUpstreamGate2=true"
		if expectedArg != dep.Spec.Template.Spec.Containers[0].Args[0] {
			t.Fatalf("args differ, container: %q, expected: %q", dep.Spec.Template.Spec.Containers[0].Args[0], expectedArg)
		}
		mockMapper.ValidateCalls(t, 0, 0, 1, 1)
	})

	t.Run("controller mapping exists with some features that include duplicates and matching container", func(t *testing.T) {
		dep := testDep.DeepCopy()
		dep.Name = operatorControllerDeploymentName
		dep.Spec.Template.Spec.Containers[0].Name = "manager"
		dep.Spec.Template.Spec.Containers[0].Args = []string{"--feature-gates=TestUpstreamGate2=true", "--feature-gates=TestUpstreamGate1=true"}

		enabledFeatures := []configv1.FeatureGateName{features.FeatureGateNewOLM, features.FeatureGateExample}
		mockAccessor := &MockFeatureGateAccessor{
			featureGate: featuregates.NewFeatureGate(
				enabledFeatures, []configv1.FeatureGateName{},
			),
			err: nil,
		}
		mockMapper := &MockFeatureGateMapper{
			ctrlDownstreamKeys: enabledFeatures,
			ctrlOut:            []string{"TestUpstreamGate1", "TestUpstreamGate2", "TestUpstreamGate1"},
		}

		update := controller.UpdateDeploymentFeatureGatesHook(mockAccessor, mockMapper)
		err := update(nil, dep)
		if err != nil {
			t.Fatalf("unexpected error in first update: %v", err)
		}
		if len(dep.Spec.Template.Spec.Containers[0].Args) != 1 {
			t.Fatalf("args length not 1: %+v", dep)
		}
		expectedArg := "--feature-gates=TestUpstreamGate1=true,TestUpstreamGate2=true"
		if expectedArg != dep.Spec.Template.Spec.Containers[0].Args[0] {
			t.Fatalf("args differ, container: %q, expected: %q", dep.Spec.Template.Spec.Containers[0].Args[0], expectedArg)
		}
		mockMapper.ValidateCalls(t, 2, 1, 0, 0)
	})

	t.Run("catalog mapping exists with some features that include duplicates and matching container", func(t *testing.T) {
		dep := testDep.DeepCopy()
		dep.Name = catalogdDeploymentName
		dep.Spec.Template.Spec.Containers[0].Name = "manager"
		dep.Spec.Template.Spec.Containers[0].Args = []string{"--feature-gates=TestUpstreamGate2=true", "--feature-gates=TestUpstreamGate1=true"}

		enabledFeatures := []configv1.FeatureGateName{features.FeatureGateNewOLM, features.FeatureGateExample}
		mockAccessor := &MockFeatureGateAccessor{
			featureGate: featuregates.NewFeatureGate(
				enabledFeatures, []configv1.FeatureGateName{},
			),
			err: nil,
		}
		mockMapper := &MockFeatureGateMapper{
			catalogDownstreamKeys: enabledFeatures,
			catalogOut:            []string{"TestUpstreamGate1", "TestUpstreamGate2", "TestUpstreamGate1"},
		}

		update := controller.UpdateDeploymentFeatureGatesHook(mockAccessor, mockMapper)
		err := update(nil, dep)
		if err != nil {
			t.Fatalf("unexpected error in first update: %v", err)
		}
		if len(dep.Spec.Template.Spec.Containers[0].Args) != 1 {
			t.Fatalf("args length not 1: %+v", dep)
		}
		expectedArg := "--feature-gates=TestUpstreamGate1=true,TestUpstreamGate2=true"
		if expectedArg != dep.Spec.Template.Spec.Containers[0].Args[0] {
			t.Fatalf("args differ, container: %q, expected: %q", dep.Spec.Template.Spec.Containers[0].Args[0], expectedArg)
		}
		mockMapper.ValidateCalls(t, 0, 0, 2, 1)
	})

	t.Run("controller mapping exists with mismatch feature gates and matching container", func(t *testing.T) {
		dep := testDep.DeepCopy()
		dep.Name = operatorControllerDeploymentName
		dep.Spec.Template.Spec.Containers[0].Name = "manager"
		dep.Spec.Template.Spec.Containers[0].Args = []string{"--feature-gates=TestUpstreamGate2=true", "--feature-gates=TestUpstreamGate3=true"}

		enabledFeatures := []configv1.FeatureGateName{features.FeatureGateNewOLM, features.FeatureGateExample}
		mockAccessor := &MockFeatureGateAccessor{
			featureGate: featuregates.NewFeatureGate(
				enabledFeatures, []configv1.FeatureGateName{},
			),
			err: nil,
		}
		mockMapper := &MockFeatureGateMapper{
			ctrlDownstreamKeys: enabledFeatures,
			ctrlOut:            []string{"TestUpstreamGate1", "TestUpstreamGate2"},
		}

		update := controller.UpdateDeploymentFeatureGatesHook(mockAccessor, mockMapper)
		err := update(nil, dep)
		if err == nil {
			t.Fatalf("expected error in update")
		}
		mockMapper.ValidateCalls(t, 2, 1, 0, 0)
	})

	t.Run("catalog mapping exists with mismatch feature gates and matching container", func(t *testing.T) {
		dep := testDep.DeepCopy()
		dep.Name = catalogdDeploymentName
		dep.Spec.Template.Spec.Containers[0].Name = "manager"
		dep.Spec.Template.Spec.Containers[0].Args = []string{"--feature-gates=TestUpstreamGate2=true", "--feature-gates=TestUpstreamGate3=true"}

		enabledFeatures := []configv1.FeatureGateName{features.FeatureGateNewOLM, features.FeatureGateExample}
		mockAccessor := &MockFeatureGateAccessor{
			featureGate: featuregates.NewFeatureGate(
				enabledFeatures, []configv1.FeatureGateName{},
			),
			err: nil,
		}
		mockMapper := &MockFeatureGateMapper{
			catalogDownstreamKeys: enabledFeatures,
			catalogOut:            []string{"TestUpstreamGate1", "TestUpstreamGate2"},
		}

		update := controller.UpdateDeploymentFeatureGatesHook(mockAccessor, mockMapper)
		err := update(nil, dep)
		if err == nil {
			t.Fatalf("expected error in update")
		}
		mockMapper.ValidateCalls(t, 0, 0, 2, 1)
	})

	t.Run("controller mapping exists with matching features over multiple arguments and matching container", func(t *testing.T) {
		dep := testDep.DeepCopy()
		dep.Name = operatorControllerDeploymentName
		dep.Spec.Template.Spec.Containers[0].Name = "manager"
		dep.Spec.Template.Spec.Containers[0].Args = []string{
			"--feature-gates=TestUpstreamGate2=true,TestUpstreamGate3=true",
			"--feature-gates=TestUpstreamGate1=true",
		}

		enabledFeatures := []configv1.FeatureGateName{features.FeatureGateNewOLM, features.FeatureGateExample}
		mockAccessor := &MockFeatureGateAccessor{
			featureGate: featuregates.NewFeatureGate(
				enabledFeatures, []configv1.FeatureGateName{},
			),
			err: nil,
		}
		mockMapper := &MockFeatureGateMapper{
			ctrlDownstreamKeys: enabledFeatures,
			ctrlOut:            []string{"TestUpstreamGate1", "TestUpstreamGate2", "TestUpstreamGate3"},
		}

		update := controller.UpdateDeploymentFeatureGatesHook(mockAccessor, mockMapper)
		err := update(nil, dep)
		if err != nil {
			t.Fatalf("unexpected error in first update: %v", err)
		}
		if len(dep.Spec.Template.Spec.Containers[0].Args) != 1 {
			t.Fatalf("args length not 1: %+v", dep)
		}
		expectedArg := "--feature-gates=TestUpstreamGate1=true,TestUpstreamGate2=true,TestUpstreamGate3=true"
		if expectedArg != dep.Spec.Template.Spec.Containers[0].Args[0] {
			t.Fatalf("args differ, container: %q, expected: %q", dep.Spec.Template.Spec.Containers[0].Args[0], expectedArg)
		}
		mockMapper.ValidateCalls(t, 2, 1, 0, 0)
	})

	t.Run("catalog mapping exists with matching features over multiple and extra arguments and matching container", func(t *testing.T) {
		dep := testDep.DeepCopy()
		dep.Name = catalogdDeploymentName
		dep.Spec.Template.Spec.Containers[0].Name = "manager"
		dep.Spec.Template.Spec.Containers[0].Args = []string{
			"--feature-gates=TestUpstreamGate2=true,TestUpstreamGate3=true",
			"--feature-gates=TestUpstreamGate1=true",
			"--other=value=true",
		}

		enabledFeatures := []configv1.FeatureGateName{features.FeatureGateNewOLM, features.FeatureGateExample}
		mockAccessor := &MockFeatureGateAccessor{
			featureGate: featuregates.NewFeatureGate(
				enabledFeatures, []configv1.FeatureGateName{},
			),
			err: nil,
		}
		mockMapper := &MockFeatureGateMapper{
			catalogDownstreamKeys: enabledFeatures,
			catalogOut:            []string{"TestUpstreamGate1", "TestUpstreamGate2", "TestUpstreamGate3"},
		}

		update := controller.UpdateDeploymentFeatureGatesHook(mockAccessor, mockMapper)
		err := update(nil, dep)
		if err != nil {
			t.Fatalf("unexpected error in first update: %v", err)
		}
		if len(dep.Spec.Template.Spec.Containers[0].Args) != 2 {
			t.Fatalf("args length not 2: %+v", dep)
		}
		// This argument is added back in, so it is last
		expectedArg := "--feature-gates=TestUpstreamGate1=true,TestUpstreamGate2=true,TestUpstreamGate3=true"
		if expectedArg != dep.Spec.Template.Spec.Containers[0].Args[1] {
			t.Fatalf("args differ, container: %q, expected: %q", dep.Spec.Template.Spec.Containers[0].Args[0], expectedArg)
		}
		expectedArg = "--other=value=true"
		if expectedArg != dep.Spec.Template.Spec.Containers[0].Args[0] {
			t.Fatalf("args differ, container: %q, expected: %q", dep.Spec.Template.Spec.Containers[0].Args[0], expectedArg)
		}
		mockMapper.ValidateCalls(t, 0, 0, 2, 1)
	})
}
