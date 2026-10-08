# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

.PHONY: test lint ci test-cover

test:
	go test ./...

lint:
	go vet ./...

ci: lint test

test-cover:
	mkdir -p test-result
	go test -coverprofile=test-result/coverage.out ./...
