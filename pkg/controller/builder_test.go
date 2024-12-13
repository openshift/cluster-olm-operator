package controller

import (
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestControllerNameForObject(t *testing.T) {
	type testCase struct {
		name     string
		prefix   string
		obj      object
		expected string
	}
	for _, tc := range []testCase{
		{
			name:   "deployment",
			prefix: "test-prefix",
			obj: &appsv1.Deployment{
				TypeMeta: metav1.TypeMeta{
					Kind: "Deployment",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-name",
					Namespace: "test-namespace",
				},
			},
			expected: "TestPrefixDeploymentTestName",
		},
		{
			name:   "configmap",
			prefix: "test-prefix",
			obj: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind: "ConfigMap",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-name",
					Namespace: "test-namespace",
				},
			},
			expected: "TestPrefixConfigMapTestName",
		},
		{
			name:   "too long",
			prefix: "test-prefix",
			obj: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind: "ConfigMap",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-name-that-is-entirely-too-long-to-be-a-valid-controller-name",
				},
			},
			expected: "TestPrefixConfigMapTestNameThatIsEntirelyTooLongToBeAValidControllerName",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual := controllerNameForObject(tc.prefix, tc.obj)
			if actual != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, actual)
			}
			if len(actual) > 63 {
				// TODO: update controllerNameForObject to avoid returning names that
				//    are longer than 63 characters
				t.Skipf("TODO: controller name %q is too long", actual)
			}
		})
	}
}

type MockProxyClient struct {
	configv1.Proxy
}

func (m *MockProxyClient) Get(_ string) (*configv1.Proxy, error) {
	return &m.Proxy, nil
}

func TestUpdateEnv(t *testing.T) {
	mpc := MockProxyClient{
		Proxy: configv1.Proxy{
			Status: configv1.ProxyStatus{
				HTTPProxy:  HTTPProxy,
				HTTPSProxy: HTTPSProxy,
				NoProxy:    NoProxy,
			},
		},
	}

	dep := appsv1.Deployment{
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

	update := UpdateDeploymentProxyHook(&mpc)
	err := update(nil, &dep)
	if err != nil {
		t.Fatalf("unexpected error in first update: %v", err)
	}
	if len(dep.Spec.Template.Spec.Containers[0].Env) != 3 {
		t.Fatalf("environment length not 3: %+v", dep)
	}

	check := func() {
		// We want to make sure the order is preserved, so check explicitly
		vars := []corev1.EnvVar{
			{Name: HTTPSProxy, Value: HTTPSProxy},
			{Name: HTTPProxy, Value: HTTPProxy},
			{Name: NoProxy, Value: NoProxy},
		}
		for i := range vars {
			if vars[i] != dep.Spec.Template.Spec.Containers[0].Env[i] {
				t.Fatalf("iter %d: expected: %+v, got: %+v", i, vars[i], dep.Spec.Template.Spec.Containers[0].Env[i])
			}
		}
	}
	check()

	err = update(nil, &dep)
	if err == nil {
		t.Fatal("no error in second update")
	}
	// Make sure the Deployment is unchanged
	check()
}
