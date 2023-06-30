SHELL := /usr/bin/env bash

GO_TEST_PACKAGES :=./pkg/... ./cmd/...
GO_BUILD_BINDIR := bin

.PHONY: all
all: build

# bingo manages consistent tooling versions for things like kind, kustomize, etc.
include .bingo/Variables.mk

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
lint: $(GOLANGCI_LINT) ## Run golangci linter.
	$(GOLANGCI_LINT) run $(GOLANGCI_LINT_ARGS)


