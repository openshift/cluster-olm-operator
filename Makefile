default: help

# bingo manages consistent tooling versions for things like kind, kustomize, etc.
include .bingo/Variables.mk

# golangci-lint on *nix uses XDG_CACHE_HOME, falling back to HOME, as the default storage directory. Some CI setups
# don't have XDG_CACHE_HOME set; in those cases, we set it here so lint functions correctly. This shouldn't
# affect developers.
export XDG_CACHE_HOME ?= /tmp/.local/cache

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "Usage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

clean: ## Remove binaries and test artifacts
	rm -rf bin

.PHONY: build
build: ## Build binaries
	mkdir -p bin && go build -o bin ./cmd/cluster-olm-operator

.PHONY: lint
lint: $(GOLANGCI_LINT) ## Run golangci linter.
	$(GOLANGCI_LINT) run $(GOLANGCI_LINT_ARGS)

include $(addprefix ./vendor/github.com/openshift/build-machinery-go/make/, \
    targets/openshift/deps.mk \
)
