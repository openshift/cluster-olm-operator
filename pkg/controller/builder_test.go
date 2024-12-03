package controller

import (
	"os"
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

func TestReplaceEnvironmentHook(t *testing.T) {
	expected := `env: [{"name":"NOT_HTTP_PROXY","value":"proxy1"},{"name":"NOT_HTTPS_PROXY","value":"proxy2"}]`
	fakeDeployment := "env: {}"
	data := []byte(fakeDeployment)
	os.Setenv("NOT_HTTP_PROXY", "proxy1")
	os.Setenv("NOT_HTTPS_PROXY", "proxy2")
	replacer := replaceEnvironmentHook(fakeDeployment, "NOT_HTTP_PROXY", "NOT_HTTPS_PROXY")
	newData, err := replacer(nil, data)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	} else {
		str := string(newData)
		if str != expected {
			t.Errorf("output=%s does not equal expected=%s", str, expected)
		}
	}
}
