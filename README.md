# cluster-olm-operator

Operator that manages the lifecycle of the Operator Lifecycle Manager (OLM) components.

The repo is for a downstream specific component

It exists as a way for us to facilitate two things:

1. To turn off and on the feature flags for olm v1 so we could ship it in the openshift payload without it being turned on by default
2. To handle the `clusterstatus` resource for the v1 components
   
Because OCP has an API that's part of the `cluster-version-operator`, that isn't in plain kubernetes, that tracks the state of all the OCP components, and if you're in the payload you are required to write status to it
