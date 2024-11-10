#!/usr/bin/env bash

set -Eeuo pipefail

cd $(git rev-parse --show-toplevel)
export DOCKER_DEFAULT_PLATFORM=linux/amd64
docker build . -t ghcr.io/mac-chaffee/ip-pass:$TAG
docker push ghcr.io/mac-chaffee/ip-pass:$TAG
git tag $TAG
git push origin --tags
