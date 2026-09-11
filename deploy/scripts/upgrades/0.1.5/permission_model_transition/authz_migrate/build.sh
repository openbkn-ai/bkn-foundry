#!/usr/bin/env bash
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

set -euo pipefail

script_directory=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cd "$script_directory"
GOTOOLCHAIN=go1.25.0 CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -buildvcs=false -trimpath -ldflags="-s -w" -o authz-migrate .
sha256sum authz-migrate > authz-migrate.sha256
