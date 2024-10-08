package resourceapply

import (
	"context"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rbacclientv1 "k8s.io/client-go/kubernetes/typed/rbac/v1"
	"k8s.io/klog/v2"

	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourcehelper"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
)

// ApplyClusterRole merges objectmeta, requires rules.
func ApplyClusterRole(ctx context.Context, client rbacclientv1.ClusterRolesGetter, recorder events.Recorder, required *rbacv1.ClusterRole) (*rbacv1.ClusterRole, bool, error) {
	existing, err := client.ClusterRoles().Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		requiredCopy := required.DeepCopy()
		actual, err := client.ClusterRoles().Create(
			ctx, resourcemerge.WithCleanLabelsAndAnnotations(requiredCopy).(*rbacv1.ClusterRole), metav1.CreateOptions{})
		resourcehelper.ReportCreateEvent(recorder, required, err)
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}

	modified := false
	existingCopy := existing.DeepCopy()

	resourcemerge.EnsureObjectMeta(&modified, &existingCopy.ObjectMeta, required.ObjectMeta)
	rulesContentSame := equality.Semantic.DeepEqual(existingCopy.Rules, required.Rules)
	aggregationRuleContentSame := equality.Semantic.DeepEqual(existingCopy.AggregationRule, required.AggregationRule)

	if aggregationRuleContentSame && rulesContentSame && !modified {
		return existingCopy, false, nil
	}

	if !aggregationRuleContentSame {
		existingCopy.AggregationRule = required.AggregationRule
	}

	// The control plane controller that reconciles ClusterRoles
	// overwrites any values that are manually specified in the rules field of an aggregate ClusterRole.
	// As such skip reconciling on the Rules field when the AggregationRule is set.
	if !rulesContentSame && required.AggregationRule == nil {
		existingCopy.Rules = required.Rules
	}

	if klog.V(2).Enabled() {
		klog.Infof("ClusterRole %q changes: %v", required.Name, JSONPatchNoError(existing, existingCopy))
	}

	actual, err := client.ClusterRoles().Update(ctx, existingCopy, metav1.UpdateOptions{})
	resourcehelper.ReportUpdateEvent(recorder, required, err)
	return actual, true, err
}

// ApplyClusterRoleBinding merges objectmeta, requires subjects and role refs
// TODO on non-matching roleref, delete and recreate
func ApplyClusterRoleBinding(ctx context.Context, client rbacclientv1.ClusterRoleBindingsGetter, recorder events.Recorder, required *rbacv1.ClusterRoleBinding) (*rbacv1.ClusterRoleBinding, bool, error) {
	existing, err := client.ClusterRoleBindings().Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		requiredCopy := required.DeepCopy()
		actual, err := client.ClusterRoleBindings().Create(
			ctx, resourcemerge.WithCleanLabelsAndAnnotations(requiredCopy).(*rbacv1.ClusterRoleBinding), metav1.CreateOptions{})
		resourcehelper.ReportCreateEvent(recorder, required, err)
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}

	modified := false
	existingCopy := existing.DeepCopy()
	requiredCopy := required.DeepCopy()

	// Enforce apiGroup fields in roleRefs
	existingCopy.RoleRef.APIGroup = rbacv1.GroupName
	for i := range existingCopy.Subjects {
		if existingCopy.Subjects[i].Kind == "User" {
			existingCopy.Subjects[i].APIGroup = rbacv1.GroupName
		}
	}

	requiredCopy.RoleRef.APIGroup = rbacv1.GroupName
	for i := range requiredCopy.Subjects {
		if requiredCopy.Subjects[i].Kind == "User" {
			requiredCopy.Subjects[i].APIGroup = rbacv1.GroupName
		}
	}

	resourcemerge.EnsureObjectMeta(&modified, &existingCopy.ObjectMeta, requiredCopy.ObjectMeta)

	subjectsAreSame := equality.Semantic.DeepEqual(existingCopy.Subjects, requiredCopy.Subjects)
	roleRefIsSame := equality.Semantic.DeepEqual(existingCopy.RoleRef, requiredCopy.RoleRef)

	if subjectsAreSame && roleRefIsSame && !modified {
		return existingCopy, false, nil
	}

	existingCopy.Subjects = requiredCopy.Subjects
	existingCopy.RoleRef = requiredCopy.RoleRef

	if klog.V(2).Enabled() {
		klog.Infof("ClusterRoleBinding %q changes: %v", requiredCopy.Name, JSONPatchNoError(existing, existingCopy))
	}

	actual, err := client.ClusterRoleBindings().Update(ctx, existingCopy, metav1.UpdateOptions{})
	resourcehelper.ReportUpdateEvent(recorder, requiredCopy, err)
	return actual, true, err
}

// ApplyRole merges objectmeta, requires rules
func ApplyRole(ctx context.Context, client rbacclientv1.RolesGetter, recorder events.Recorder, required *rbacv1.Role) (*rbacv1.Role, bool, error) {
	existing, err := client.Roles(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		requiredCopy := required.DeepCopy()
		actual, err := client.Roles(required.Namespace).Create(
			ctx, resourcemerge.WithCleanLabelsAndAnnotations(requiredCopy).(*rbacv1.Role), metav1.CreateOptions{})
		resourcehelper.ReportCreateEvent(recorder, required, err)
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}

	modified := false
	existingCopy := existing.DeepCopy()

	resourcemerge.EnsureObjectMeta(&modified, &existingCopy.ObjectMeta, required.ObjectMeta)
	contentSame := equality.Semantic.DeepEqual(existingCopy.Rules, required.Rules)
	if contentSame && !modified {
		return existingCopy, false, nil
	}

	existingCopy.Rules = required.Rules

	if klog.V(2).Enabled() {
		klog.Infof("Role %q changes: %v", required.Namespace+"/"+required.Name, JSONPatchNoError(existing, existingCopy))
	}
	actual, err := client.Roles(required.Namespace).Update(ctx, existingCopy, metav1.UpdateOptions{})
	resourcehelper.ReportUpdateEvent(recorder, required, err)
	return actual, true, err
}

// ApplyRoleBinding merges objectmeta, requires subjects and role refs
// TODO on non-matching roleref, delete and recreate
func ApplyRoleBinding(ctx context.Context, client rbacclientv1.RoleBindingsGetter, recorder events.Recorder, required *rbacv1.RoleBinding) (*rbacv1.RoleBinding, bool, error) {
	existing, err := client.RoleBindings(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		requiredCopy := required.DeepCopy()
		actual, err := client.RoleBindings(required.Namespace).Create(
			ctx, resourcemerge.WithCleanLabelsAndAnnotations(requiredCopy).(*rbacv1.RoleBinding), metav1.CreateOptions{})
		resourcehelper.ReportCreateEvent(recorder, required, err)
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}

	modified := false
	existingCopy := existing.DeepCopy()
	requiredCopy := required.DeepCopy()

	// Enforce apiGroup fields in roleRefs and subjects
	existingCopy.RoleRef.APIGroup = rbacv1.GroupName
	for i := range existingCopy.Subjects {
		if existingCopy.Subjects[i].Kind == "User" {
			existingCopy.Subjects[i].APIGroup = rbacv1.GroupName
		}
	}

	requiredCopy.RoleRef.APIGroup = rbacv1.GroupName
	for i := range requiredCopy.Subjects {
		if requiredCopy.Subjects[i].Kind == "User" {
			requiredCopy.Subjects[i].APIGroup = rbacv1.GroupName
		}
	}

	resourcemerge.EnsureObjectMeta(&modified, &existingCopy.ObjectMeta, requiredCopy.ObjectMeta)

	subjectsAreSame := equality.Semantic.DeepEqual(existingCopy.Subjects, requiredCopy.Subjects)
	roleRefIsSame := equality.Semantic.DeepEqual(existingCopy.RoleRef, requiredCopy.RoleRef)

	if subjectsAreSame && roleRefIsSame && !modified {
		return existingCopy, false, nil
	}

	existingCopy.Subjects = requiredCopy.Subjects
	existingCopy.RoleRef = requiredCopy.RoleRef

	if klog.V(2).Enabled() {
		klog.Infof("RoleBinding %q changes: %v", requiredCopy.Namespace+"/"+requiredCopy.Name, JSONPatchNoError(existing, existingCopy))
	}

	actual, err := client.RoleBindings(requiredCopy.Namespace).Update(ctx, existingCopy, metav1.UpdateOptions{})
	resourcehelper.ReportUpdateEvent(recorder, requiredCopy, err)
	return actual, true, err
}

func DeleteClusterRole(ctx context.Context, client rbacclientv1.ClusterRolesGetter, recorder events.Recorder, required *rbacv1.ClusterRole) (*rbacv1.ClusterRole, bool, error) {
	err := client.ClusterRoles().Delete(ctx, required.Name, metav1.DeleteOptions{})
	if err != nil && apierrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	resourcehelper.ReportDeleteEvent(recorder, required, err)
	return nil, true, nil
}

func DeleteClusterRoleBinding(ctx context.Context, client rbacclientv1.ClusterRoleBindingsGetter, recorder events.Recorder, required *rbacv1.ClusterRoleBinding) (*rbacv1.ClusterRoleBinding, bool, error) {
	err := client.ClusterRoleBindings().Delete(ctx, required.Name, metav1.DeleteOptions{})
	if err != nil && apierrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	resourcehelper.ReportDeleteEvent(recorder, required, err)
	return nil, true, nil
}

func DeleteRole(ctx context.Context, client rbacclientv1.RolesGetter, recorder events.Recorder, required *rbacv1.Role) (*rbacv1.Role, bool, error) {
	err := client.Roles(required.Namespace).Delete(ctx, required.Name, metav1.DeleteOptions{})
	if err != nil && apierrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	resourcehelper.ReportDeleteEvent(recorder, required, err)
	return nil, true, nil
}

func DeleteRoleBinding(ctx context.Context, client rbacclientv1.RoleBindingsGetter, recorder events.Recorder, required *rbacv1.RoleBinding) (*rbacv1.RoleBinding, bool, error) {
	err := client.RoleBindings(required.Namespace).Delete(ctx, required.Name, metav1.DeleteOptions{})
	if err != nil && apierrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	resourcehelper.ReportDeleteEvent(recorder, required, err)
	return nil, true, nil
}
