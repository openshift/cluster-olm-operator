// Package controller provides deployment hooks for configuring TLS security profiles
// and proxy settings from OpenShift cluster configuration.
package controller

import (
	"context"
	"errors"
	"fmt"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/deploymentcontroller"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-olm-operator/pkg/clients"
)

// Proxy environment variable constants
const (
	HTTPProxy  = "HTTP_PROXY"
	HTTPSProxy = "HTTPS_PROXY"
	NoProxy    = "NO_PROXY"
)

func UpdateDeploymentProxyHook(pc clients.ProxyClientInterface) deploymentcontroller.DeploymentHookFunc {
	return func(_ *operatorv1.OperatorSpec, deployment *appsv1.Deployment) error {
		klog.FromContext(context.Background()).WithName("builder").V(1).Info("ProxyHook updating environment", "deployment", deployment.Name)
		proxyConfig, err := pc.Get("cluster")
		if err != nil {
			return fmt.Errorf("error getting proxies.config.openshift.io/cluster: %w", err)
		}

		vars := []corev1.EnvVar{
			{Name: HTTPSProxy, Value: proxyConfig.Status.HTTPSProxy},
			{Name: HTTPProxy, Value: proxyConfig.Status.HTTPProxy},
			{Name: NoProxy, Value: proxyConfig.Status.NoProxy},
		}

		var errs []error
		for i := range deployment.Spec.Template.Spec.InitContainers {
			err = setContainerEnv(&deployment.Spec.Template.Spec.InitContainers[i], vars)
			if err != nil {
				errs = append(errs, err)
			}
		}
		for i := range deployment.Spec.Template.Spec.Containers {
			err = setContainerEnv(&deployment.Spec.Template.Spec.Containers[i], vars)
			if err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) > 0 {
			return errors.Join(errs...)
		}

		return nil
	}
}

func updateEnv(con *corev1.Container, env corev1.EnvVar) error {
	for _, e := range con.Env {
		if e.Name == env.Name {
			return fmt.Errorf("unexpected environment variable %q=%q in container %q while building manifests", e.Name, e.Value, con.Name)
		}
	}
	if env.Value == "" {
		return nil
	}
	klog.FromContext(context.Background()).WithName("builder").V(4).Info("Updated environment", "container", con.Name, "key", env.Name, "value", env.Value)
	con.Env = append(con.Env, env)
	return nil
}

func setContainerEnv(con *corev1.Container, envs []corev1.EnvVar) error {
	for _, env := range envs {
		if err := updateEnv(con, env); err != nil {
			return err
		}
	}
	return nil
}
