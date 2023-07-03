FROM registry.ci.openshift.org/ocp/builder:rhel-8-golang-1.20-openshift-4.14 AS builder
WORKDIR /build
COPY . .
RUN make build

FROM registry.ci.openshift.org/ocp/4.14:base

COPY --from=builder /build/bin/cluster-olm-operator /

USER 1001

LABEL io.openshift.release.operator=true \
      io.k8s.display-name="OpenShift Cluster Operator Lifecycle Manager (OLM) Operator" \
      io.k8s.description="This cluster-olm-operator installs and maintains the Operator Lifecycle Manager (OLM) components of the OCP cluster."
