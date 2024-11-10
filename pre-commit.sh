#!/usr/bin/env bash
set -Eeuo pipefail
cd $(git rev-parse --show-toplevel)
go fmt ./pkg
goimports -w ./pkg
go test ./pkg
EOF
