# cluster-olm-operator

Operator that manages the lifecycle of the Operator Lifecycle Manager (OLM) components.

The repo is for a downstream specific component

It exists as a way for us to facilitate two things:

1. To turn off and on the feature flags for olm v1 so we could ship it in the openshift payload without it being turned on by default
2. To handle the `clusterstatus` resource for the v1 components
   
Because OCP has an API that's part of the `cluster-version-operator`, that isn't in plain kubernetes, that tracks the state of all the OCP components, and if you're in the payload you are required to write status to it

## Managed resource cleanup

Rendered operand resources carry `olm.openshift.io/managed-by: cluster-olm-operator`
on their own metadata. This identifies resources reconciled by this operator,
including existing resources it adopts. Pod templates and selectors are unchanged.

Automatic pruning compares the complete rendered identity set with live objects
carrying the ownership label. Diagnostic `relatedObjects` are not a deletion
inventory. The controller polls once per minute, with shorter retries while
cleanup is pending, and adds no resource watches. The caller supplies the
namespaces; production uses `openshift-catalogd` and
`openshift-operator-controller`. Cluster-scoped resources are listed separately.
Supplemental Roles and RoleBindings
grant ConfigMap, Secret and ServiceMonitor deletion only in those operand
namespaces; those delete permissions are not granted cluster-wide.
The explicit resource-type scopes remain configured after a kind disappears
from the chart. Add new types to `DefaultPruningScopes` with corresponding RBAC.
Namespaces, CRDs, ClusterExtensions and ClusterObjectSets are retained.

Pruning acts only in Managed state. Deletes use UID and resourceVersion
preconditions to protect concurrent ownership changes. Pending deletion reports
Progressing and respects finalizers; API errors report Degraded. Pruning waits
for every rendered Deployment to complete a successful apply in
the current operator process. Controller startup and status from an earlier
process cannot release this barrier. Managed catalog cleanup then completes
before pruning operand deployments.
An unavailable API stops pruning rather than being interpreted as an empty list.

The ownership-marking commit can be backported independently of pruning.
A separate explicit-deletion change handles legacy resource retirement. Automatic
pruning cannot identify an unlabelled resource removed before the marking release
ran; retain explicit retirement entries for those resources. Pruning does not
require an external inventory or Helm release history.
