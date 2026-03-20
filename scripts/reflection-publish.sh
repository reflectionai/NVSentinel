#!/usr/bin/env bash
# Builds and publishes NVSentinel images and Helm chart to Reflection's GCP
# Artifact Registry at us-central1-docker.pkg.dev/reflectionai-gpu-platform.
#
# Usage:
#   ./scripts/reflection-publish.sh [--skip-auth]

set -euo pipefail

SKIP_AUTH=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --skip-auth) SKIP_AUTH=true; shift ;;
        *) echo "Unknown flag: $1" >&2; exit 1 ;;
    esac
done

TAG=$(git rev-parse --short HEAD)

export CONTAINER_REGISTRY=us-central1-docker.pkg.dev
export CONTAINER_ORG=reflectionai-gpu-platform
export CI_COMMIT_REF_NAME="${TAG}"
CHART_VERSION=$(grep "^version:" distros/kubernetes/nvsentinel/Chart.yaml | awk '{print $2}')
export SAFE_REF="${CHART_VERSION}-${TAG}"
export HELM_OCI_REGISTRY=us-central1-docker.pkg.dev
export HELM_OCI_REPOSITORY=reflectionai-gpu-platform/nvsentinel
export DISABLE_REGISTRY_CACHE=true
export HELM_EXPERIMENTAL_OCI=1
export PLATFORMS="linux/$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')"

gcloud auth print-access-token \
    | helm registry login us-central1-docker.pkg.dev \
        -u oauth2accesstoken --password-stdin

gcloud artifacts repositories describe nvsentinel \
    --location=us-central1 \
    --project=reflectionai-gpu-platform &>/dev/null \
    || gcloud artifacts repositories create nvsentinel \
        --repository-format=docker \
        --location=us-central1 \
        --project=reflectionai-gpu-platform

make docker-setup-buildx
make docker-publish-all
make kubernetes-distro-helm-publish

echo "Done. Tag: ${TAG}"
