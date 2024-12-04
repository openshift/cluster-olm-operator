package controller

import (
	"testing"

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
