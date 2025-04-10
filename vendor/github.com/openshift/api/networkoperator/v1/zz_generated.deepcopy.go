//go:build !ignore_autogenerated
// +build !ignore_autogenerated

// Code generated by codegen. DO NOT EDIT.

package v1

import (
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *EgressRouter) DeepCopyInto(out *EgressRouter) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new EgressRouter.
func (in *EgressRouter) DeepCopy() *EgressRouter {
	if in == nil {
		return nil
	}
	out := new(EgressRouter)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *EgressRouter) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *EgressRouterAddress) DeepCopyInto(out *EgressRouterAddress) {
	*out = *in
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new EgressRouterAddress.
func (in *EgressRouterAddress) DeepCopy() *EgressRouterAddress {
	if in == nil {
		return nil
	}
	out := new(EgressRouterAddress)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *EgressRouterInterface) DeepCopyInto(out *EgressRouterInterface) {
	*out = *in
	out.Macvlan = in.Macvlan
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new EgressRouterInterface.
func (in *EgressRouterInterface) DeepCopy() *EgressRouterInterface {
	if in == nil {
		return nil
	}
	out := new(EgressRouterInterface)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *EgressRouterList) DeepCopyInto(out *EgressRouterList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]EgressRouter, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new EgressRouterList.
func (in *EgressRouterList) DeepCopy() *EgressRouterList {
	if in == nil {
		return nil
	}
	out := new(EgressRouterList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *EgressRouterList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *EgressRouterSpec) DeepCopyInto(out *EgressRouterSpec) {
	*out = *in
	if in.Redirect != nil {
		in, out := &in.Redirect, &out.Redirect
		*out = new(RedirectConfig)
		(*in).DeepCopyInto(*out)
	}
	out.NetworkInterface = in.NetworkInterface
	if in.Addresses != nil {
		in, out := &in.Addresses, &out.Addresses
		*out = make([]EgressRouterAddress, len(*in))
		copy(*out, *in)
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new EgressRouterSpec.
func (in *EgressRouterSpec) DeepCopy() *EgressRouterSpec {
	if in == nil {
		return nil
	}
	out := new(EgressRouterSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *EgressRouterStatus) DeepCopyInto(out *EgressRouterStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]EgressRouterStatusCondition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new EgressRouterStatus.
func (in *EgressRouterStatus) DeepCopy() *EgressRouterStatus {
	if in == nil {
		return nil
	}
	out := new(EgressRouterStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *EgressRouterStatusCondition) DeepCopyInto(out *EgressRouterStatusCondition) {
	*out = *in
	in.LastTransitionTime.DeepCopyInto(&out.LastTransitionTime)
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new EgressRouterStatusCondition.
func (in *EgressRouterStatusCondition) DeepCopy() *EgressRouterStatusCondition {
	if in == nil {
		return nil
	}
	out := new(EgressRouterStatusCondition)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *L4RedirectRule) DeepCopyInto(out *L4RedirectRule) {
	*out = *in
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new L4RedirectRule.
func (in *L4RedirectRule) DeepCopy() *L4RedirectRule {
	if in == nil {
		return nil
	}
	out := new(L4RedirectRule)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *MacvlanConfig) DeepCopyInto(out *MacvlanConfig) {
	*out = *in
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new MacvlanConfig.
func (in *MacvlanConfig) DeepCopy() *MacvlanConfig {
	if in == nil {
		return nil
	}
	out := new(MacvlanConfig)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RedirectConfig) DeepCopyInto(out *RedirectConfig) {
	*out = *in
	if in.RedirectRules != nil {
		in, out := &in.RedirectRules, &out.RedirectRules
		*out = make([]L4RedirectRule, len(*in))
		copy(*out, *in)
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RedirectConfig.
func (in *RedirectConfig) DeepCopy() *RedirectConfig {
	if in == nil {
		return nil
	}
	out := new(RedirectConfig)
	in.DeepCopyInto(out)
	return out
}
