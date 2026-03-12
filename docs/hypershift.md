# HyperShift Support

cluster-olm-operator supports running in HyperShift mode, where it manages OLMv1 components (catalogd and operator-controller) for hosted clusters.

## Overview

In HyperShift deployments, cluster-olm-operator can run in the management cluster and manage OLMv1 components that watch hosted cluster API servers. This enables:

- catalogd to serve catalogs from the management cluster while watching ClusterCatalog resources in the hosted cluster's API server
- operator-controller to install operators into hosted cluster worker nodes while watching ClusterExtension resources in the hosted cluster's API server

This corresponds to **Approach 1: Control Plane Placement** as described in the [HyperShift OLMv1 design document](https://github.com/openshift/enhancements/blob/master/enhancements/olm/hypershift-olmv1.md).

## Architecture

### Standalone Mode (Default)

In standalone OpenShift clusters:
- cluster-olm-operator runs in `openshift-cluster-olm-operator` namespace
- catalogd and operator-controller watch the local cluster's API server using in-cluster config
- Components run in `olmv1-system` namespace

### HyperShift Mode

In HyperShift deployments:
- cluster-olm-operator runs in the management cluster (in the hosted control plane namespace, e.g., `clusters-customer1`)
- catalogd and operator-controller watch the **hosted cluster's** API server using a mounted kubeconfig
- Components are configured with `--kubeconfig` and `--system-namespace` flags
- The `admin-kubeconfig` secret provides connectivity to the hosted cluster's API server

## Configuration

HyperShift mode is enabled by setting environment variables on the cluster-olm-operator deployment:

### Required Environment Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `HOSTED_KUBECONFIG_SECRET` | Name of the secret containing the hosted cluster's kubeconfig | `admin-kubeconfig` |
| `HOSTED_NAMESPACE` | The hosted control plane namespace in the management cluster | `clusters-customer1` |

### Example Deployment Configuration

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cluster-olm-operator
  namespace: clusters-customer1  # Hosted control plane namespace
spec:
  template:
    spec:
      containers:
      - name: cluster-olm-operator
        image: quay.io/openshift/origin-cluster-olm-operator:latest
        env:
        - name: HOSTED_KUBECONFIG_SECRET
          value: admin-kubeconfig
        - name: HOSTED_NAMESPACE
          value: clusters-customer1
        # ... other environment variables ...
```

## How It Works

When HyperShift mode is detected (via `HOSTED_KUBECONFIG_SECRET` environment variable):

1. **Kubeconfig Injection Hook**: The `InjectHostedClusterKubeconfigHook` deployment hook is automatically applied to catalogd and operator-controller deployments

2. **Volume Mounting**: The hook adds a volume referencing the kubeconfig secret:
   ```yaml
   volumes:
   - name: hosted-kubeconfig
     secret:
       secretName: admin-kubeconfig  # Value from HOSTED_KUBECONFIG_SECRET
   ```

3. **Volume Mounts**: The kubeconfig is mounted into all containers:
   ```yaml
   volumeMounts:
   - name: hosted-kubeconfig
     mountPath: /var/run/secrets/kubeconfig
     readOnly: true
   ```

4. **Command-line Flags**: Additional arguments are added to containers:
   ```yaml
   args:
   - --kubeconfig=/var/run/secrets/kubeconfig/kubeconfig
   - --system-namespace=clusters-customer1  # Value from HOSTED_NAMESPACE
   ```

## Components Affected

The HyperShift configuration is automatically applied to:

- **catalogd**: Watches ClusterCatalog resources in the hosted cluster's API server
- **operator-controller**: Watches ClusterExtension resources in the hosted cluster's API server and installs operators into hosted cluster worker nodes

Both components continue to serve their control plane functions from the management cluster while interacting with hosted cluster API resources.

## Upstream Requirements

For HyperShift mode to work, the upstream components must support:

- **catalogd**: `--kubeconfig` flag support ([catalogd PR #xyz](https://github.com/operator-framework/catalogd/pull/xyz))
- **operator-controller**: `--kubeconfig` flag support ([operator-controller PR #xyz](https://github.com/operator-framework/operator-controller/pull/xyz))
- Both components: `--system-namespace` flag to specify the namespace context

## Detection and Logging

When cluster-olm-operator starts in HyperShift mode:

```
I0312 10:15:23.123456       1 builder.go:150] HyperShift mode detected, injecting kubeconfig configuration deployment="catalogd" kubeconfigSecret="admin-kubeconfig" hostedNamespace="clusters-customer1"
I0312 10:15:23.234567       1 builder.go:150] HyperShift mode detected, injecting kubeconfig configuration deployment="operator-controller" kubeconfigSecret="admin-kubeconfig" hostedNamespace="clusters-customer1"
```

Individual deployment hooks also log their actions:

```
I0312 10:15:23.345678       1 builder.go:354] Injecting hosted cluster kubeconfig configuration deployment="catalogd" kubeconfigSecret="admin-kubeconfig" hostedNamespace="clusters-customer1"
I0312 10:15:23.456789       1 builder.go:380] Configured container container="catalogd" kubeconfigPath="/var/run/secrets/kubeconfig/kubeconfig" systemNamespace="clusters-customer1"
```

## Verification

To verify cluster-olm-operator is running in HyperShift mode:

1. Check environment variables:
   ```bash
   kubectl get deployment cluster-olm-operator -n clusters-customer1 -o yaml | grep -A2 HOSTED_
   ```

2. Check catalogd/operator-controller deployments for kubeconfig configuration:
   ```bash
   kubectl get deployment catalogd -n clusters-customer1 -o yaml | grep -A5 "hosted-kubeconfig"
   kubectl get deployment operator-controller -n clusters-customer1 -o yaml | grep "kubeconfig"
   ```

3. Verify components are watching the hosted cluster API:
   ```bash
   # Check catalogd logs
   kubectl logs -n clusters-customer1 deployment/catalogd | grep "kubeconfig"

   # Check operator-controller logs
   kubectl logs -n clusters-customer1 deployment/operator-controller | grep "kubeconfig"
   ```

## Troubleshooting

### Components not connecting to hosted cluster

**Symptoms**: catalogd or operator-controller cannot list resources, API connection errors

**Checks**:
1. Verify the `admin-kubeconfig` secret exists and is properly mounted
2. Check the secret contains a valid kubeconfig
3. Verify network connectivity from management cluster to hosted cluster API server
4. Check RBAC permissions in the kubeconfig

### Missing environment variables

**Symptoms**: Components use in-cluster config instead of hosted cluster kubeconfig

**Solution**: Ensure both `HOSTED_KUBECONFIG_SECRET` and `HOSTED_NAMESPACE` environment variables are set on the cluster-olm-operator deployment

### Hook not applied

**Symptoms**: Deployments don't have kubeconfig volumes or --kubeconfig flags

**Checks**:
1. Verify environment variables are set before cluster-olm-operator starts
2. Check cluster-olm-operator logs for "HyperShift mode detected" messages
3. Verify the deployment controller is processing deployments correctly

## References

- [HyperShift OLMv1 Design Proposal](https://github.com/openshift/enhancements/blob/master/enhancements/olm/hypershift-olmv1.md)
- [catalogd Documentation](https://github.com/operator-framework/catalogd)
- [operator-controller Documentation](https://github.com/operator-framework/operator-controller)
- [HyperShift Documentation](https://hypershift-docs.netlify.app/)
