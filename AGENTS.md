# AGENTS.md

## Overview

The `cluster-olm-operator` is an OpenShift-specific operator that manages the lifecycle of Operator Lifecycle Manager (OLM) v1 components on OpenShift clusters. This is a downstream-specific component that serves as a bridge between OpenShift's cluster management infrastructure and the upstream OLMv1 project.

## Architecture

### Purpose

This operator exists to facilitate two primary functions:

1. **Feature Flag Management**: Control the enablement/disablement of OLMv1 features in OpenShift, allowing OLMv1 to be shipped in the OpenShift payload without being enabled by default
2. **Cluster Status Reporting**: Maintain the `ClusterOperator` resource status for OLM components, which is required by OpenShift's `cluster-version-operator` to track the health of all OpenShift components

### Source Components

The operator deploys OLMv1 components that originate from the upstream [operator-framework/operator-controller](https://github.com/operator-framework/operator-controller) repository, which are then maintained in OpenShift's downstream fork at [github.com/openshift/operator-framework-operator-controller](https://github.com/openshift/operator-framework-operator-controller).

The two main OLMv1 components managed by this operator are:

- **catalogd**: The catalog management component that provides catalog services (image: `quay.io/openshift/origin-olm-catalogd`)
- **operator-controller**: The operator installation and lifecycle management component (image: `quay.io/openshift/origin-olm-operator-controller`)

## Deployment Mechanism

### Init Container Pattern

The operator uses a unique deployment pattern with init containers to obtain Helm manifests:

1. **copy-catalogd-manifests**: Extracts Helm chart templates from the catalogd image
2. **copy-operator-controller-manifests**: Extracts Helm chart templates from the operator-controller image

Both init containers execute the `/cp-manifests` script to copy their respective Helm charts to a shared `emptyDir` volume at `/operand-assets`.

### Runtime Helm Rendering

The main operator container (`cluster-olm-operator`) then:

1. Reads the Helm charts from `/operand-assets/helm/{catalogd,operator-controller}`
2. Evaluates cluster feature gates and configuration
3. Selects appropriate values files:
   - `openshift.yaml`: Standard production configuration
   - `experimental.yaml`: Additional configuration when TechPreview/CustomNoUpgrade/DevPreviewNoUpgrade feature sets are enabled
4. Renders Helm templates with dynamic values including:
   - `CATALOGD_IMAGE`: The catalogd container image reference
   - `OPERATOR_CONTROLLER_IMAGE`: The operator-controller container image reference
   - Proxy configuration
   - Feature gates
   - Log verbosity levels
5. Deploys the rendered manifests to the cluster

### Expected Helm Chart Structure

```
/operand-assets/helm/${component}/
├── olmv1/                    # Helm chart directory
│   ├── Chart.yaml
│   ├── templates/
│   │   ├── deployment.yaml
│   │   ├── service.yaml
│   │   └── ...
│   └── values.yaml
├── openshift.yaml           # Standard values file
└── experimental.yaml        # Feature-gated values file
```

## Controllers

The operator implements several specialized controllers:

### 1. Static Resource Controllers
- Manage immutable Kubernetes resources (Namespaces, ServiceAccounts, ClusterRoles, etc.)
- One controller per component (`CatalogdStaticResources`, `OperatorControllerStaticResources`)

### 2. Deployment Controllers
- Manage Deployment resources with dynamic manifest hooks
- Apply proxy configuration via `UpdateDeploymentProxyHook`
- Apply operator configuration via `UpdateDeploymentObservedConfigHook`
- Adjust log verbosity based on operator log level

### 3. Dynamic Resource Controllers
- Handle `ClusterCatalog` custom resources
- Use the `DynamicRequiredManifestController` for runtime resource management

### 4. Status Controllers

#### ClusterOperator Status Controller
- Maintains the `olm` ClusterOperator resource
- Reports the health and version of OLMv1 components
- Tracks related objects for must-gather support

#### Static Upgradeable Condition Controller
- Monitors the state of all managed controllers
- Updates the operator's upgradeable condition

#### Incompatible Operator Controller
- Scans installed `ClusterExtension` resources for upgrade compatibility
- Checks for `olm.maxOpenShiftVersion` property to detect operators incompatible with cluster upgrades
- Sets the `InstalledOLMOperatorsUpgradeable` condition to prevent cluster upgrades when incompatible operators are detected

### 5. Configuration Controllers

#### Proxy Controller
- Watches the cluster `Proxy` configuration
- Updates operator environment variables when proxy settings change
- Ensures OLMv1 components respect cluster-wide proxy configuration

#### TLS Observer Controller
- Monitors the cluster `APIServer` TLS security profile
- Updates operator configuration to match cluster TLS requirements

## Feature Gate Management

The operator supports OpenShift feature gates that can enable experimental or preview OLMv1 features:

- **Default**: Standard OLMv1 features only
- **TechPreviewNoUpgrade**: Enables technical preview features
- **CustomNoUpgrade**: Enables custom feature configurations
- **DevPreviewNoUpgrade**: Enables developer preview features

Feature gates are mapped from OpenShift's downstream feature gate names to upstream OLMv1 feature gate names via the `FeatureGateMapper`.

## Key Files

- **cmd/cluster-olm-operator/main.go**: Main entry point, controller initialization, `olms` resource reference setup
- **pkg/controller/builder.go**: Controller builder, Helm rendering orchestration
- **pkg/controller/helm.go**: Helm template rendering logic
- **pkg/controller/observedconfig_hook.go**: Reads TLS configuration from `olms.spec.observedConfig`
- **pkg/controller/dynamicrequiredmanifest_controller.go**: Dynamic resource management
- **pkg/controller/incompatible_operator_controller.go**: Upgrade compatibility checks, writes to `olms.status`
- **pkg/controller/proxycontroller.go**: Proxy configuration management
- **pkg/controller/tlsobserver.go**: TLS profile observation, updates `olms.spec.observedConfig`
- **pkg/helmvalues/**: Helm values manipulation utilities
- **pkg/clients/clients.go**: Client setup including `OperatorClient` for `olms` resource
- **manifests/**: Operator deployment manifests

## Environment Variables

The operator relies on the following environment variables:

- `CATALOGD_IMAGE`: Container image for catalogd deployment
- `OPERATOR_CONTROLLER_IMAGE`: Container image for operator-controller deployment
- `KUBE_RBAC_PROXY_IMAGE`: Container image for kube-rbac-proxy sidecar
- `OPERATOR_IMAGE_VERSION`: Version of the cluster-olm-operator itself

## Development

### Building
```bash
make build
```

### Testing
```bash
make test-unit
```

### Rapid Development with Tilt
See [CONTRIBUTING.md](CONTRIBUTING.md) for instructions on using Tilt for rapid iterative development.

## Integration with OpenShift

### The OLM Custom Resource (`olms`)

The central configuration point for this operator is the `OLM` custom resource (kind: `OLM`, resource path: `olms`):

- **API**: `operator.openshift.io/v1alpha1`
- **Scope**: Cluster-scoped singleton
- **Name**: Must be `cluster` (enforced by validation)
- **Feature Gate**: Behind the `NewOLM` feature gate

#### Purpose

The `olms.operator.openshift.io` resource serves as:

1. **Configuration Input**: The operator reads its configuration from the `spec` field, including:
   - Management state (Managed, Unmanaged, Removed)
   - Log level configuration
   - Observed configuration (TLS security profiles, proxy settings)

2. **Status Output**: The operator writes operational status to the `status` field, including:
   - Conditions (Available, Progressing, Degraded, Upgradeable)
   - Observed generation
   - Operator version information

#### Observed Configuration

The operator reads the `spec.observedConfig` field to extract runtime configuration:

- **TLS Security Profile** (`olmTLSSecurityProfile`): Minimum TLS version and cipher suites
  - Translated to `--tls-custom-version`, `--tls-custom-ciphers`, and `--tls-profile=custom` container arguments
  - Applied to catalogd and operator-controller deployments via `UpdateDeploymentObservedConfigHook`

##### How observedConfig is Populated

Currently, only **TLS configuration** is written to `observedConfig`. The `observedConfig` is written by the **TLS Observer Controller** (pkg/controller/tlsobserver.go:62-96):

1. Watches the cluster `APIServer` resource (`config.openshift.io/v1`)
2. Uses library-go's `apiserver.ObserveTLSSecurityProfileWithPaths()` function to extract TLS settings
3. Writes the observed TLS configuration to `olms.spec.observedConfig` at the path `olmTLSSecurityProfile`
4. Includes two fields:
   - `minTLSVersion`: From APIServer's `.spec.tlsSecurityProfile.minTLSVersion` (pkg/controller/tlsobserver.go:23-25)
   - `cipherSuites`: From APIServer's `.spec.tlsSecurityProfile.ciphers` (pkg/controller/tlsobserver.go:27-30)

The **UpdateDeploymentObservedConfigHook** (pkg/controller/observedconfig_hook.go:22-54) then reads this configuration and applies it to OLMv1 component deployments.

##### Proxy Configuration (NOT in observedConfig)

**Important**: Proxy settings are handled differently and do **not** go through `observedConfig`:

- **Proxy Controller** (pkg/controller/proxycontroller.go:17-37): Reads the cluster `Proxy` resource and updates the cluster-olm-operator's own process environment variables (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`) via `os.Setenv()` (pkg/controller/proxycontroller.go:39-62)

- **UpdateDeploymentProxyHook** (pkg/controller/proxy_hook.go:25-58): Reads directly from the `Proxy` resource and injects proxy environment variables into OLMv1 component containers and init containers, bypassing the `observedConfig` mechanism entirely

### OpenShift Payload

This operator is shipped as part of the OpenShift release payload and runs in the `openshift-cluster-olm-operator` namespace. It has a priority class of `system-cluster-critical` and runs on master nodes with appropriate tolerations.

### ClusterOperator Resource

The operator maintains the `olm` ClusterOperator resource at `/apis/config.openshift.io/v1/clusteroperators/olm`, which reports:

- Available: Whether OLMv1 is successfully deployed
- Progressing: Whether changes are being applied
- Degraded: Whether any components are unhealthy
- Upgradeable: Whether the cluster can be upgraded (blocked by incompatible operators)

### Must-Gather Support

The operator configures related objects in the ClusterOperator status to ensure must-gather picks up relevant resources for troubleshooting:

- The `olms.operator.openshift.io` custom resource (named `cluster`)
- The `openshift-cluster-olm-operator` namespace
- All deployed OLMv1 components (catalogd, operator-controller, etc.)

## Dependencies

Key dependencies (see [go.mod](go.mod)):

- `github.com/openshift/api`: OpenShift API types
- `github.com/openshift/library-go`: OpenShift operator utilities
- `github.com/operator-framework/operator-controller`: Operator controller and catalogd API types
- `helm.sh/helm/v3`: Helm SDK for template rendering
- `k8s.io/client-go`: Kubernetes client libraries

## Related Repositories

- **Upstream OLMv1**: https://github.com/operator-framework/operator-controller
- **Downstream OLMv1 Fork**: https://github.com/openshift/operator-framework-operator-controller
- **OpenShift API**: https://github.com/openshift/api
- **OpenShift Library-Go**: https://github.com/openshift/library-go
