#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..

CODEGEN_PKG=$(go list -f '{{.Dir}}' -m k8s.io/code-generator)
if [[ -z "${CODEGEN_PKG}" ]]; then
  echo "Failed to get k8s.io/code-generator path"
  exit 1
fi

source "${CODEGEN_PKG}/kube_codegen.sh"

THIS_PKG="github.com/cobaltcore-dev/cortex"

kube::codegen::gen_helpers \
    --boilerplate "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
    "${SCRIPT_ROOT}"

kube::codegen::gen_client \
    --with-watch \
    --output-dir "${SCRIPT_ROOT}/pkg/generated" \
    --output-pkg "${THIS_PKG}/pkg/generated" \
    --boilerplate "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
    "${SCRIPT_ROOT}"
