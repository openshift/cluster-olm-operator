load('ext://restart_process', 'docker_build_with_restart')

build_cmd = """
mkdir -p .tiltbuild/bin
CGO_ENABLED=0 GOOS=linux go build -o .tiltbuild/bin/cluster-olm-operator ./cmd/cluster-olm-operator
"""

# Treat the main binary as a local resource, so we can automatically rebuild it when any of the deps change. This builds
# it locally, targeting linux, so it can run in a linux container.
local_resource(
    'cluster-olm-operator_binary',
    cmd = build_cmd,
    deps = ['cmd', 'pkg', 'vendor', 'go.mod', 'go.sum']
)

# Use a custom Dockerfile specifically for Tilt.
dockerfile = """
FROM gcr.io/distroless/base:debug
WORKDIR /
COPY cluster-olm-operator .
"""

# Configure our image build. If the file in live_update.sync (.tiltbuild/bin/cluster-olm-operator) changes, Tilt
# copies it to the running container and restarts it.
docker_build_with_restart(
    # This has to match an image in the k8s_yaml we call below, so Tilt knows to use this image for our Deployment,
    # instead of the actual image specified in the yaml.
    ref = 'quay.io/openshift/origin-cluster-olm-operator:latest',
    # This is the `docker build` context, and because we're only copying in the binary we've already had Tilt build
    # locally, we set the context to the directory containing the binary.
    context = '.tiltbuild/bin',
    # The Tilt-specific Dockerfile from above.
    dockerfile_contents = dockerfile,
    # The set of files Tilt should include in the build. In this case, it's just the binary we built above.
    only = 'cluster-olm-operator',
    live_update = [
        # If .tiltbuild/bin/cluster-olm-operator is modified, sync it into the container and restart the process.
        sync('.tiltbuild/bin/cluster-olm-operator', '/cluster-olm-operator'),
    ],
    # The command to run in the container.
    entrypoint = "/cluster-olm-operator start -v=2",
)

# We have to tell Tilt what to deploy. This is roughly equivalent to `kubectl apply -f manifests` but we have to tell
# Tilt about each file individually.
for f in listdir('manifests'):
    if not f.endswith('.yaml'):
        continue
    if f.endswith('deployment.yaml'):
        objects = read_yaml_stream(f)
        for o in objects:
            # For Tilt's live_update functionality to work, we have to run the container as root. Otherwise, Tilt won't
            # be able to untar on top of /cluster-olm-operator in the container's file system (this is how live update
            # works).
            o['spec']['template']['spec']['securityContext']['runAsNonRoot'] = False
        k8s_yaml(encode_yaml_stream(objects))
    else:
        k8s_yaml(f)
