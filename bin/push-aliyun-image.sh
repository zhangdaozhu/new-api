#!/usr/bin/env bash
set -euo pipefail

REGISTRY="${REGISTRY:-registry.cn-hangzhou.aliyuncs.com}"
NAMESPACE="${NAMESPACE:-devblockchain}"
IMAGE_NAME="${IMAGE_NAME:-new-api}"
TAG="${TAG:-latest}"
PLATFORM="${PLATFORM:-linux/amd64}"
DOCKERFILE="${DOCKERFILE:-Dockerfile}"
CONTEXT_DIR="${CONTEXT_DIR:-.}"

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

need_cmd docker

if ! docker buildx version >/dev/null 2>&1; then
  echo "Docker buildx is required." >&2
  exit 1
fi

IMAGE_URI="${REGISTRY}/${NAMESPACE}/${IMAGE_NAME}:${TAG}"

echo "Building and pushing ${IMAGE_URI}"
docker buildx build \
  --platform "${PLATFORM}" \
  -f "${DOCKERFILE}" \
  -t "${IMAGE_URI}" \
  --push \
  "${CONTEXT_DIR}"

echo "Pushed ${IMAGE_URI}"
