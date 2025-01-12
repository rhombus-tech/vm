#!/usr/bin/env bash
# Copyright (C) 2023, Ava Labs, Inc. All rights reserved.
# See the file LICENSE for licensing terms.

set -e

# to run E2E tests (terminates cluster afterwards)
# MODE=test ./scripts/run.sh
MODE=${MODE:-run}
if ! [[ "$0" =~ scripts/run.sh ]]; then
  echo "must be run from morpheusvm root"
  exit 255
fi

# shellcheck source=/scripts/constants.sh
source ./scripts/common/constants.sh
# shellcheck source=/scripts/common/utils.sh
source ./scripts/common/utils.sh

VERSION=v1.11.12-rc.2

# TEE Configuration
TEE_ENABLED=${TEE_ENABLED:-true}
SGX_ENDPOINT=${SGX_ENDPOINT:-"localhost:50051"}
SEV_ENDPOINT=${SEV_ENDPOINT:-"localhost:50052"}
TEE_CONFIG=""

if [[ "${TEE_ENABLED}" == "true" ]]; then
    TEE_CONFIG="--tee.sgx-endpoint=${SGX_ENDPOINT} --tee.sev-endpoint=${SEV_ENDPOINT}"
    
    # Launch TEE services if they're not already running
    if ! pgrep -f "tee-service.*sgx" > /dev/null; then
        echo "Starting SGX TEE service..."
        docker run -d --name sgx-tee -p "${SGX_ENDPOINT}:50051" morpheus/tee-service:latest --mode=sgx
    fi
    
    if ! pgrep -f "tee-service.*sev" > /dev/null; then
        echo "Starting SEV TEE service..."
        docker run -d --name sev-tee -p "${SEV_ENDPOINT}:50052" morpheus/tee-service:latest --mode=sev
    fi
    
    # Wait for TEE services to be ready
    echo "Waiting for TEE services to be ready..."
    sleep 5
fi

############################
# build avalanchego
# https://github.com/ava-labs/avalanchego/releases
HYPERSDK_DIR=$HOME/.hypersdk

echo "working directory: $HYPERSDK_DIR"

AVALANCHEGO_PATH=${HYPERSDK_DIR}/avalanchego-${VERSION}/avalanchego
AVALANCHEGO_PLUGIN_DIR=${HYPERSDK_DIR}/avalanchego-${VERSION}/plugins

if [ ! -f "$AVALANCHEGO_PATH" ]; then
  echo "building avalanchego"
  CWD=$(pwd)

  # Clear old folders
  rm -rf "${HYPERSDK_DIR}"/avalanchego-"${VERSION}"
  mkdir -p "${HYPERSDK_DIR}"/avalanchego-"${VERSION}"
  rm -rf "${HYPERSDK_DIR}"/avalanchego-src
  mkdir -p "${HYPERSDK_DIR}"/avalanchego-src

  # Download src
  cd "${HYPERSDK_DIR}"/avalanchego-src
  git clone https://github.com/ava-labs/avalanchego.git
  cd avalanchego
  git checkout "${VERSION}"

  # Build avalanchego
  ./scripts/build.sh
  mv build/avalanchego "${HYPERSDK_DIR}"/avalanchego-"${VERSION}"

  cd "${CWD}"

  # Clear src
  rm -rf "${HYPERSDK_DIR}"/avalanchego-src
else
  echo "using previously built avalanchego"
fi

############################

echo "building morpheusvm"

# delete previous (if exists)
rm -f "${HYPERSDK_DIR}"/avalanchego-"${VERSION}"/plugins/qCNyZHrs3rZX458wPJXPJJypPf6w423A84jnfbdP2TPEmEE9u

# rebuild with latest code
go build \
-o "${HYPERSDK_DIR}"/avalanchego-"${VERSION}"/plugins/qCNyZHrs3rZX458wPJXPJJypPf6w423A84jnfbdP2TPEmEE9u \
./cmd/morpheusvm

############################
echo "building e2e.test"

prepare_ginkgo

ACK_GINKGO_RC=true ginkgo build ./tests/e2e
./tests/e2e/e2e.test --help

additional_args=("$@")

if [[ ${MODE} == "run" ]]; then
  echo "applying ginkgo.focus=Ping and --reuse-network to setup local network"
  additional_args+=("--ginkgo.focus=Ping")
  additional_args+=("--reuse-network")
fi

# Add TEE configuration to test arguments if enabled
if [[ "${TEE_ENABLED}" == "true" ]]; then
    additional_args+=("--tee.enabled=true")
    additional_args+=("--tee.sgx-endpoint=${SGX_ENDPOINT}")
    additional_args+=("--tee.sev-endpoint=${SEV_ENDPOINT}")
fi

echo "running e2e tests"
./tests/e2e/e2e.test \
--ginkgo.v \
--avalanchego-path="${AVALANCHEGO_PATH}" \
--plugin-dir="${AVALANCHEGO_PLUGIN_DIR}" \
${TEE_CONFIG} \
"${additional_args[@]}"

# Cleanup TEE services if they were started
if [[ "${MODE}" == "test" && "${TEE_ENABLED}" == "true" ]]; then
    echo "Cleaning up TEE services..."
    docker stop sgx-tee sev-tee || true
    docker rm sgx-tee sev-tee || true
fi