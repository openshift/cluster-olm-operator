package controller

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// Write supplemental manifests before building controllers so permissions follow
// the same apply, ownership and desired-resource paths as the operand resources.
func writePruningRBAC(dir string, namespaces []string) error {
	found := map[string]bool{}
	if err := WalkYAMLManifestsDir(dir, func(_ string, obj *unstructured.Unstructured, _ []byte) error {
		ns := obj.GetNamespace()
		if obj.GetKind() == "Namespace" && obj.GetAPIVersion() == "v1" {
			ns = obj.GetName()
		}
		if slices.Contains(namespaces, ns) {
			found[ns] = true
		}
		return nil
	}); err != nil {
		return err
	}
	for ns := range found {
		name := "cluster-olm-operator-pruning"
		role := rbacv1.Role{TypeMeta: metav1.TypeMeta{APIVersion: rbacv1.SchemeGroupVersion.String(), Kind: "Role"}, ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"configmaps", "secrets"}, Verbs: []string{"delete"}},
			{APIGroups: []string{"monitoring.coreos.com"}, Resources: []string{"servicemonitors"}, Verbs: []string{"delete"}},
		}}
		binding := rbacv1.RoleBinding{TypeMeta: metav1.TypeMeta{APIVersion: rbacv1.SchemeGroupVersion.String(), Kind: "RoleBinding"}, ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: name}, Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: "cluster-olm-operator", Namespace: "openshift-cluster-olm-operator"}}}
		for i, obj := range []interface{}{role, binding} {
			data, err := yaml.Marshal(obj)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("zz-pruning-%s-%d.yaml", ns, i)), data, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
