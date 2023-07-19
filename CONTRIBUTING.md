# How to Contribute

This project is [Apache 2.0 licensed](LICENSE) and accepts contributions via
GitHub pull requests.

## Certificate of Origin

By contributing to this project you agree to the Developer Certificate of
Origin (DCO). This document was created by the Linux Kernel community and is a
simple statement that you, as a contributor, have the legal right to make the
contribution. See the [DCO](DCO) file for details.

# Rapid iterative development with Tilt

[Tilt](https://tilt.dev) is a tool that enables rapid iterative development of containerized workloads.

Here is an example workflow without Tilt for modifying some source code and testing those changes in a cluster:

1. Modify the source code.
2. Build the container image.
3. Either push the image to a registry or load it into your kind cluster.
4. Deploy all the appropriate Kubernetes manifests for your application.
   1. Or, if this is an update, you'd instead scale the Deployment to 0 replicas, scale back to 1, and wait for the 
      new pod to be running.

This process can take minutes, depending on how long each step takes.

Here is the same workflow with Tilt:

1. Run `tilt up`
2. Modify the source code
3. Wait for Tilt to update the container with your changes

This ends up taking a fraction of the time, sometimes on the order of a few seconds!

## Installing Tilt

Follow Tilt's [instructions](https://docs.tilt.dev/install.html) for installation.

## Starting Tilt

This is typically a short as:

```shell
tilt up
```

**NOTE:** if you are using Podman, at least as of v4.5.1, you need to do this:

```shell
DOCKER_BUILDKIT=0 tilt up
```

Otherwise, you'll see an error when Tilt tries to build your image that looks similar to:

```shell
Build Failed: ImageBuild: stat /var/tmp/libpod_builder2384046170/build/Dockerfile: no such file or directory
```

When Tilt starts, you'll see something like this in your terminal:

```shell
Tilt started on http://localhost:10350/
v0.33.1, built 2023-06-28

(space) to open the browser
(s) to stream logs (--stream=true)
(t) to open legacy terminal mode (--legacy=true)
(ctrl-c) to exit
```

Typically, you'll want to press the space bar to have it open the UI in your web browser.

Shortly after starting, Tilt processes the `Tiltfile`, resulting in:

- Building the go binary
- Deploying everything in `manifests` except the Deployment that references the main image
- Building the main image
- Loading the image into kind
- Modifying the Deployment to use the just-built image
- Creating the Deployment

## Making code changes

Any time you change any of the files listed in the `deps` section in the `cluster-olm-operator_binary` `local_resource`,
Tilt automatically rebuilds the go binary. As soon as the binary is rebuilt, Tilt pushes it (and only it) into the 
running container, and then restarts the process.
