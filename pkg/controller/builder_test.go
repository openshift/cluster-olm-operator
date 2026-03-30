package controller

import (
	"reflect"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

func TestWalkYAMLManifestsDir(t *testing.T) {
	t.Run("", func(t *testing.T) {
		manifestsDir := "../../manifests"
		testRestMapper := map[schema.GroupVersionKind]*meta.RESTMapping{
			operatorv1.GroupVersion.WithKind("OLM"): {
				Resource:         operatorv1.GroupVersion.WithResource("olms"),
				GroupVersionKind: operatorv1.GroupVersion.WithKind("OLM"),
				Scope:            meta.RESTScopeRoot,
			},
			corev1.SchemeGroupVersion.WithKind("Namespace"): {
				Resource:         corev1.SchemeGroupVersion.WithResource("namespaces"),
				GroupVersionKind: corev1.SchemeGroupVersion.WithKind("Namespace"),
				Scope:            meta.RESTScopeRoot,
			},
			corev1.SchemeGroupVersion.WithKind("Service"): {
				Resource:         corev1.SchemeGroupVersion.WithResource("services"),
				GroupVersionKind: corev1.SchemeGroupVersion.WithKind("Service"),
				Scope:            meta.RESTScopeNamespace,
			},
			corev1.SchemeGroupVersion.WithKind("ServiceAccount"): {
				Resource:         corev1.SchemeGroupVersion.WithResource("serviceaccounts"),
				GroupVersionKind: corev1.SchemeGroupVersion.WithKind("ServiceAccount"),
				Scope:            meta.RESTScopeNamespace,
			},
			rbacv1.SchemeGroupVersion.WithKind("ClusterRole"): {
				Resource:         rbacv1.SchemeGroupVersion.WithResource("clusterroles"),
				GroupVersionKind: rbacv1.SchemeGroupVersion.WithKind("ClusterRole"),
				Scope:            meta.RESTScopeRoot,
			},
			rbacv1.SchemeGroupVersion.WithKind("ClusterRoleBinding"): {
				Resource:         rbacv1.SchemeGroupVersion.WithResource("clusterrolebindings"),
				GroupVersionKind: rbacv1.SchemeGroupVersion.WithKind("ClusterRoleBinding"),
				Scope:            meta.RESTScopeRoot,
			},
			appsv1.SchemeGroupVersion.WithKind("Deployment"): {
				Resource:         appsv1.SchemeGroupVersion.WithResource("deployments"),
				GroupVersionKind: appsv1.SchemeGroupVersion.WithKind("Deployment"),
				Scope:            meta.RESTScopeNamespace,
			},
			networkingv1.SchemeGroupVersion.WithKind("NetworkPolicy"): {
				Resource:         networkingv1.SchemeGroupVersion.WithResource("networkpolicies"),
				GroupVersionKind: networkingv1.SchemeGroupVersion.WithKind("NetworkPolicy"),
				Scope:            meta.RESTScopeNamespace,
			},
			configv1.SchemeGroupVersion.WithKind("ClusterOperator"): {
				Resource:         configv1.SchemeGroupVersion.WithResource("clusteroperators"),
				GroupVersionKind: configv1.SchemeGroupVersion.WithKind("ClusterOperator"),
				Scope:            meta.RESTScopeRoot,
			},
		}

		expectedRefs := []configv1.ObjectReference{
			{
				Group:    operatorv1.GroupName,
				Resource: "olms",
				Name:     "cluster",
			},
			{
				Resource: "namespaces",
				Name:     "openshift-cluster-olm-operator",
			},
			{
				Group:    rbacv1.GroupName,
				Resource: "clusterroles",
				Name:     "cluster-olm-operator",
			},
			{
				Resource:  "serviceaccounts",
				Namespace: "openshift-cluster-olm-operator",
				Name:      "cluster-olm-operator",
			},
			{
				Resource:  "services",
				Namespace: "openshift-cluster-olm-operator",
				Name:      "cluster-olm-operator-metrics",
			},
			{
				Group:    rbacv1.GroupName,
				Resource: "clusterrolebindings",
				Name:     "cluster-olm-operator-role",
			},
			{
				Group:     appsv1.GroupName,
				Resource:  "deployments",
				Namespace: "openshift-cluster-olm-operator",
				Name:      "cluster-olm-operator",
			},
			{
				Group:    configv1.GroupName,
				Resource: "clusteroperators",
				Name:     "olm",
			},
			{
				Group:     networkingv1.GroupName,
				Resource:  "networkpolicies",
				Namespace: "openshift-cluster-olm-operator",
				Name:      "default-deny-all",
			},
			{
				Group:     networkingv1.GroupName,
				Resource:  "networkpolicies",
				Namespace: "openshift-cluster-olm-operator",
				Name:      "allow-egress-to-openshift-dns",
			},
			{
				Group:     networkingv1.GroupName,
				Resource:  "networkpolicies",
				Namespace: "openshift-cluster-olm-operator",
				Name:      "allow-egress-to-api-server",
			},
			{
				Group:     networkingv1.GroupName,
				Resource:  "networkpolicies",
				Namespace: "openshift-cluster-olm-operator",
				Name:      "allow-metrics-traffic",
			},
		}
		actualRefs := []configv1.ObjectReference{}
		err := WalkYAMLManifestsDir(manifestsDir, func(path string, manifest *unstructured.Unstructured, _ []byte) error {
			ref, err := ToObjectReference(manifest, nil, testRestMapper)
			if err != nil {
				t.Errorf("ToObjectReference %s: err should be nil, got %v", path, err)
			}
			if ref == nil {
				return nil
			}
			actualRefs = append(actualRefs, *ref)
			return nil
		})
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if !reflect.DeepEqual(expectedRefs, actualRefs) {
			t.Errorf("expected %+v, got %+v", expectedRefs, actualRefs)
		}
	})
}
