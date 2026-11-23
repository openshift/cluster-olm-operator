SHELL := /usr/bin/env bash

GO_TEST_PACKAGES :=./pkg/... ./cmd/... ./internal/...
GO_BUILD_BINDIR := bin

.PHONY: all
all: build

include $(addprefix ./vendor/github.com/openshift/build-machinery-go/make/, \
    golang.mk \
    targets/openshift/deps.mk \
)

# golangci-lint on *nix uses XDG_CACHE_HOME, falling back to HOME, as the default storage directory. Some CI setups
# don't have XDG_CACHE_HOME set; in those cases, we set it here so lint functions correctly. This shouldn't
# affect developers.
export XDG_CACHE_HOME ?= /tmp/.local/cache

clean: ## Remove binaries and test artifacts
	rm -rf bin

.PHONY: lint
lint: ## Run golangci linter.
	go tool golangci-lint run $(GOLANGCI_LINT_ARGS)

HELM_OUTPUT := $(shell mktemp -d)
export HELM_OUTPUT
.PHONY: helm-test-output
helm-test-output:
	@echo Output helm to $(HELM_OUTPUT)
	go tool helm template olmv1 testdata/olmv1 --values testdata/openshift.yaml > $(HELM_OUTPUT)/hello-world.yaml

test-unit: helm-test-output
