package controller

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func TestPruningRBACStaysInConfiguredOperandNamespaces(t *testing.T) {
	dir := t.TempDir()
	for _, ns := range []string{"catalogd", "controller", "unrelated"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, ns+".yaml"), []byte("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: "+ns+"\n"), 0o600))
	}
	require.NoError(t, writePruningRBAC(dir, []string{"catalogd", "controller"}))
	roles, bindings := 0, 0
	require.NoError(t, WalkYAMLManifestsDir(dir, func(_ string, obj *unstructured.Unstructured, data []byte) error {
		switch obj.GetKind() {
		case "Role":
			roles++
			require.Contains(t, []string{"catalogd", "controller"}, obj.GetNamespace())
			var role rbacv1.Role
			require.NoError(t, yaml.Unmarshal(data, &role))
			require.Equal(t, []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"configmaps", "secrets"}, Verbs: []string{"delete"}}, {APIGroups: []string{"monitoring.coreos.com"}, Resources: []string{"servicemonitors"}, Verbs: []string{"delete"}}}, role.Rules)
		case "RoleBinding":
			bindings++
			require.Contains(t, []string{"catalogd", "controller"}, obj.GetNamespace())
			var binding rbacv1.RoleBinding
			require.NoError(t, yaml.Unmarshal(data, &binding))
			require.Equal(t, "Role", binding.RoleRef.Kind)
			require.Equal(t, "cluster-olm-operator-pruning", binding.RoleRef.Name)
			require.Equal(t, []rbacv1.Subject{{Kind: "ServiceAccount", Name: "cluster-olm-operator", Namespace: "openshift-cluster-olm-operator"}}, binding.Subjects)
		}
		return nil
	}))
	require.Equal(t, 2, roles)
	require.Equal(t, 2, bindings)
}
