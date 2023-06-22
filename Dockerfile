FROM registry.ci.openshift.org/ocp/builder:rhel-8-golang-1.20-openshift-4.14 AS builder
WORKDIR /build
COPY . .
RUN make build

FROM registry.ci.openshift.org/ocp/4.14:base

COPY --from=builder /build/bin/cluster-olm-operator /

LABEL io.k8s.display-name="OpenShift Operator Lifecycle Manager Catalog Controller" \
      io.k8s.description="This is a component of OpenShift Container Platform that provides operator catalog support."
