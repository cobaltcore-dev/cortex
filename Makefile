# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

.PHONY: all
all: crds deepcopy lint test

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	"$(GOLANGCI_LINT)" run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	"$(GOLANGCI_LINT)" run --fix

.PHONY: test
test: ## Run all tests.
	go test ./...

.PHONY: crds
crds: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd:allowDangerousTypes=true webhook paths="./..." output:crd:artifacts:config=helm/library/cortex/files/crds

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations as well as client, informer and lister implementations.
	$(CONTROLLER_GEN) crd:allowDangerousTypes=true object:headerFile="hack/boilerplate.go.txt" paths="./..."
	hack/generate-code.sh

.PHONY: dekustomize
dekustomize:
	@echo "Backing up stuff that shouldn't be overridden by kubebuilder-helm..."
	@TEMP_DIR=$$(mktemp -d); \
	if [ -d "dist/chart/templates/rbac" ]; then \
		cp -r dist/chart/templates/rbac "$$TEMP_DIR/rbac"; \
	fi; \
	if [ -d "dist/chart/templates/prometheus" ]; then \
		cp -r dist/chart/templates/prometheus "$$TEMP_DIR/prometheus"; \
	fi; \
	if [ -d "dist/chart/templates/metrics" ]; then \
		cp -r dist/chart/templates/metrics "$$TEMP_DIR/metrics"; \
	fi; \
	if [ -d ".github" ]; then \
		cp -r .github "$$TEMP_DIR/github"; \
	fi; \
	echo "Generating helm chart..."; \
	kubebuilder edit --plugins=helm/v1-alpha; \
	echo "Restoring stuff that shouldn't be overridden by kubebuilder-helm..."; \
	if [ -d "$$TEMP_DIR/rbac" ]; then \
		rm -rf dist/chart/templates/rbac; \
		cp -r "$$TEMP_DIR/rbac" dist/chart/templates/rbac; \
	fi; \
	if [ -d "$$TEMP_DIR/prometheus" ]; then \
		rm -rf dist/chart/templates/prometheus; \
		cp -r "$$TEMP_DIR/prometheus" dist/chart/templates/prometheus; \
	fi; \
	if [ -d "$$TEMP_DIR/metrics" ]; then \
		rm -rf dist/chart/templates/metrics; \
		cp -r "$$TEMP_DIR/metrics" dist/chart/templates/metrics; \
	fi; \
	if [ -d "$$TEMP_DIR/github" ]; then \
		rm -rf .github; \
		cp -r "$$TEMP_DIR/github" .github; \
	fi; \
	rm -rf "$$TEMP_DIR"; \
	echo "Directories restored successfully."

##@ Build

.PHONY: build
build: manifests generate dekustomize

LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint

CONTROLLER_TOOLS_VERSION ?= v0.20.0
GOLANGCI_LINT_VERSION ?= v2.8.0

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f $(1) || true ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv $(1) $(1)-$(3) ;\
} ;\
ln -sf $(1)-$(3) $(1)
endef
