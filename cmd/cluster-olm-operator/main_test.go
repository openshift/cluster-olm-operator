package main

import (
	"context"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-olm-operator/pkg/clients"
)

const (
	testNamespace      = "openshift-operator-controller"
	testServiceAccount = "object-controller-controller-manager"
)

func namespaceObj(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func serviceAccountObj(namespace, name string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
}

func clusterRoleBindingObj(name string) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

// allowSARReactor makes every SubjectAccessReview create return Allowed=true.
func allowSARReactor(client *fake.Clientset) {
	client.PrependReactor("create", "subjectaccessreviews", func(action clienttesting.Action) (bool, runtime.Object, error) {
		sar := action.(clienttesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sar.Status.Allowed = true
		return true, sar, nil
	})
}

func TestObjectControllerRendered(t *testing.T) {
	saRef := configv1.ObjectReference{Resource: "serviceaccounts", Namespace: testNamespace, Name: objectControllerServiceAccount}

	t.Run("true when the ServiceAccount was rendered", func(t *testing.T) {
		related := []configv1.ObjectReference{
			{Resource: "deployments", Namespace: testNamespace, Name: "operator-controller-controller-manager"},
			saRef,
		}
		assert.True(t, objectControllerRendered(related))
	})

	t.Run("false when nothing was rendered for it (older operator-controller image)", func(t *testing.T) {
		related := []configv1.ObjectReference{
			{Resource: "serviceaccounts", Namespace: testNamespace, Name: "operator-controller-controller-manager"},
		}
		assert.False(t, objectControllerRendered(related))
	})

	t.Run("false when name matches but the resource is not a ServiceAccount", func(t *testing.T) {
		related := []configv1.ObjectReference{
			{Resource: "deployments", Namespace: testNamespace, Name: objectControllerServiceAccount},
		}
		assert.False(t, objectControllerRendered(related))
	})

	t.Run("false when the namespace differs", func(t *testing.T) {
		related := []configv1.ObjectReference{
			{Resource: "serviceaccounts", Namespace: "somewhere-else", Name: objectControllerServiceAccount},
		}
		assert.False(t, objectControllerRendered(related))
	})
}

func TestWaitForComponentResources(t *testing.T) {
	t.Run("returns nil when namespace and ServiceAccount already exist", func(t *testing.T) {
		client := fake.NewClientset(
			namespaceObj(testNamespace),
			serviceAccountObj(testNamespace, testServiceAccount),
		)

		err := waitForComponentResources(context.Background(), &clients.Clients{KubeClient: client}, testServiceAccount, klog.Background())
		require.NoError(t, err)
	})

	t.Run("errors when the ServiceAccount never appears", func(t *testing.T) {
		// Namespace exists but the SA does not; the SA poll should give up when the context expires.
		client := fake.NewClientset(namespaceObj(testNamespace))

		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()

		err := waitForComponentResources(ctx, &clients.Clients{KubeClient: client}, testServiceAccount, klog.Background())
		require.Error(t, err)
	})
}

func TestWaitForComponentRBAC(t *testing.T) {
	t.Run("returns the found ClusterRoleBinding once SAR is allowed", func(t *testing.T) {
		crbName := objectControllerClusterAdminRoleBindingName
		client := fake.NewClientset(clusterRoleBindingObj(crbName))
		allowSARReactor(client)

		found, err := waitForComponentRBAC(context.Background(), &clients.Clients{KubeClient: client}, testServiceAccount, []string{crbName}, klog.Background())
		require.NoError(t, err)
		assert.Equal(t, crbName, found)
	})

	t.Run("selects the existing binding among several candidates", func(t *testing.T) {
		// Only the second candidate exists; the function should report that one.
		existing := "operator-controller-manager-rolebinding"
		client := fake.NewClientset(clusterRoleBindingObj(existing))
		allowSARReactor(client)

		candidates := []string{"operator-controller-cluster-admin-rolebinding", existing}
		found, err := waitForComponentRBAC(context.Background(), &clients.Clients{KubeClient: client}, operatorControllerServiceAccount, candidates, klog.Background())
		require.NoError(t, err)
		assert.Equal(t, existing, found)
	})

	t.Run("errors when no ClusterRoleBinding ever appears", func(t *testing.T) {
		client := fake.NewClientset()
		allowSARReactor(client)

		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()

		_, err := waitForComponentRBAC(ctx, &clients.Clients{KubeClient: client}, testServiceAccount, []string{objectControllerClusterAdminRoleBindingName}, klog.Background())
		require.Error(t, err)
	})

	t.Run("keeps polling until SAR becomes allowed", func(t *testing.T) {
		crbName := objectControllerClusterAdminRoleBindingName
		client := fake.NewClientset(clusterRoleBindingObj(crbName))

		var calls int
		client.PrependReactor("create", "subjectaccessreviews", func(action clienttesting.Action) (bool, runtime.Object, error) {
			sar := action.(clienttesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
			calls++
			// Deny the first evaluation, allow afterwards, to exercise the propagation poll.
			sar.Status.Allowed = calls > 1
			return true, sar, nil
		})

		found, err := waitForComponentRBAC(context.Background(), &clients.Clients{KubeClient: client}, testServiceAccount, []string{crbName}, klog.Background())
		require.NoError(t, err)
		assert.Equal(t, crbName, found)
		assert.GreaterOrEqual(t, calls, 2)
	})
}
