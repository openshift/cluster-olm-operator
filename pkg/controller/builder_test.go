package controller

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/events"
	ocv1 "github.com/operator-framework/operator-controller/api/v1"
	"github.com/stretchr/testify/require"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/utils/clock"

	"github.com/openshift/cluster-olm-operator/pkg/clients"
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

func TestBuildControllersRelatedObjects(t *testing.T) {
	const (
		catalogdServiceAccount = `apiVersion: v1
kind: ServiceAccount
metadata:
  name: catalogd-controller-manager
  namespace: openshift-catalogd
`
		operatorControllerServiceAccount = `apiVersion: v1
kind: ServiceAccount
metadata:
  name: operator-controller-controller-manager
  namespace: openshift-operator-controller
`
		objectControllerServiceAccount = `apiVersion: v1
kind: ServiceAccount
metadata:
  name: object-controller-controller-manager
  namespace: openshift-operator-controller
`
	)
	clusterCatalog := func(name string) string {
		return `apiVersion: olm.operatorframework.io/v1
kind: ClusterCatalog
metadata:
  name: ` + name + `
spec:
  source:
    type: Image
    image:
      ref: example.com/catalogs/` + name + `:latest
`
	}

	// Named references for rendered non-ClusterCatalog resources are retained (the operator relies
	// on the rendered ServiceAccounts at startup), while ClusterCatalogs and ClusterExtensions are
	// reported once each by group/resource only, so must-gather collects every instance.
	expectedRelatedObjects := []configv1.ObjectReference{
		{Resource: "serviceaccounts", Namespace: "openshift-catalogd", Name: "catalogd-controller-manager"},
		{Resource: "serviceaccounts", Namespace: "openshift-operator-controller", Name: "object-controller-controller-manager"},
		{Resource: "serviceaccounts", Namespace: "openshift-operator-controller", Name: "operator-controller-controller-manager"},
		{Group: "olm.operatorframework.io", Resource: "clustercatalogs"},
		{Group: "olm.operatorframework.io", Resource: "clusterextensions"},
	}

	for _, tc := range []struct {
		name                       string
		catalogdTemplates          map[string]string
		expectedCatalogControllers []string
	}{
		{
			name: "rendered default ClusterCatalogs are reported by group/resource only",
			catalogdTemplates: map[string]string{
				"serviceaccount.yaml":           catalogdServiceAccount,
				"clustercatalog-community.yaml": clusterCatalog("openshift-community-operators"),
				"clustercatalog-redhat.yaml":    clusterCatalog("openshift-redhat-operators"),
			},
			expectedCatalogControllers: []string{
				"CatalogdClusterCatalogOpenshiftCommunityOperators",
				"CatalogdClusterCatalogOpenshiftRedhatOperators",
			},
		},
		{
			name: "ClusterCatalogs are reported without rendered ClusterCatalog manifests",
			catalogdTemplates: map[string]string{
				"serviceaccount.yaml": catalogdServiceAccount,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assets := t.TempDir()
			writeTestHelmChart(t, filepath.Join(assets, "helm", "catalogd"), tc.catalogdTemplates)
			writeTestHelmChart(t, filepath.Join(assets, "helm", "operator-controller"), map[string]string{
				"serviceaccount-object-controller.yaml":   objectControllerServiceAccount,
				"serviceaccount-operator-controller.yaml": operatorControllerServiceAccount,
			})

			b := newTestBuilder(t, assets)
			staticResourceControllers, deploymentControllers, clusterCatalogControllers, relatedObjects, err := b.BuildControllers("catalogd", "operator-controller")
			require.NoError(t, err)

			require.Equal(t, expectedRelatedObjects, relatedObjects)

			// Reconciliation of the rendered ClusterCatalogs and static resources is unchanged.
			require.ElementsMatch(t, tc.expectedCatalogControllers, slices.Collect(maps.Keys(clusterCatalogControllers)))
			require.Contains(t, staticResourceControllers, "CatalogdStaticResources")
			require.Contains(t, staticResourceControllers, "OperatorControllerStaticResources")
			require.Empty(t, deploymentControllers)
		})
	}
}

func TestCoveredByAllInstancesRelatedObject(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ref      configv1.ObjectReference
		expected bool
	}{
		{
			name:     "named ClusterCatalog",
			ref:      configv1.ObjectReference{Group: "olm.operatorframework.io", Resource: "clustercatalogs", Name: "openshift-redhat-operators"},
			expected: true,
		},
		{
			name:     "named ClusterExtension",
			ref:      configv1.ObjectReference{Group: "olm.operatorframework.io", Resource: "clusterextensions", Name: "my-extension"},
			expected: true,
		},
		{
			name:     "ClusterObjectSet",
			ref:      configv1.ObjectReference{Group: "olm.operatorframework.io", Resource: "clusterobjectsets", Name: "my-extension-1"},
			expected: false,
		},
		{
			name:     "ClusterCatalog CustomResourceDefinition",
			ref:      configv1.ObjectReference{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions", Name: "clustercatalogs.olm.operatorframework.io"},
			expected: false,
		},
		{
			name:     "ServiceAccount",
			ref:      configv1.ObjectReference{Resource: "serviceaccounts", Namespace: "openshift-operator-controller", Name: "operator-controller-controller-manager"},
			expected: false,
		},
		{
			name:     "Secret",
			ref:      configv1.ObjectReference{Resource: "secrets", Namespace: "openshift-operator-controller", Name: "some-secret"},
			expected: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, coveredByAllInstancesRelatedObject(tc.ref))
		})
	}
}

// writeTestHelmChart writes a minimal chart in the layout renderHelmTemplate expects:
// ${dir}/olmv1/ holds the chart and ${dir}/openshift.yaml is the primary values file.
func writeTestHelmChart(t *testing.T, dir string, templates map[string]string) {
	t.Helper()
	templatesDir := filepath.Join(dir, "olmv1", "templates")
	require.NoError(t, os.MkdirAll(templatesDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "olmv1", "Chart.yaml"), []byte("apiVersion: v2\nname: olmv1\nversion: 0.0.1\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "openshift.yaml"), []byte("{}\n"), 0o600))
	for name, content := range templates {
		require.NoError(t, os.WriteFile(filepath.Join(templatesDir, name), []byte(content), 0o600))
	}
}

// newTestBuilder returns a Builder whose clients are never used to contact an API server while
// building controllers: REST mappings for the rendered kinds are provided up front.
func newTestBuilder(t *testing.T, assets string) *Builder {
	t.Helper()
	restConfig := &rest.Config{Host: "https://127.0.0.1:1"}
	cc := &controllercmd.ControllerContext{
		KubeConfig:        restConfig,
		ProtoKubeConfig:   restConfig,
		EventRecorder:     events.NewInMemoryRecorder("test", clock.RealClock{}),
		OperatorNamespace: "openshift-cluster-olm-operator",
	}
	cl, err := clients.New(cc)
	require.NoError(t, err)

	clusterCatalogGVK := ocv1.GroupVersion.WithKind("ClusterCatalog")
	serviceAccountGVK := corev1.SchemeGroupVersion.WithKind("ServiceAccount")
	return &Builder{
		Assets:            assets,
		Clients:           cl,
		ControllerContext: cc,
		KnownRESTMappings: map[schema.GroupVersionKind]*meta.RESTMapping{
			clusterCatalogGVK: {
				Resource:         ocv1.GroupVersion.WithResource("clustercatalogs"),
				GroupVersionKind: clusterCatalogGVK,
				Scope:            meta.RESTScopeRoot,
			},
			serviceAccountGVK: {
				Resource:         corev1.SchemeGroupVersion.WithResource("serviceaccounts"),
				GroupVersionKind: serviceAccountGVK,
				Scope:            meta.RESTScopeNamespace,
			},
		},
	}
}
