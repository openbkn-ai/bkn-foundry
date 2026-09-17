// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package conf

import (
	"os"
	"strings"
)

type HTTPServerConfig struct {
	Address         string
	InternalAddress string
}

func NewHTTPServerConfig() HTTPServerConfig {
	config := HTTPServerConfig{
		Address:         ":8080",
		InternalAddress: ":8081",
	}
	if value := strings.TrimSpace(os.Getenv("BKN_TRACE_HTTP_ADDRESS")); value != "" {
		config.Address = value
	}
	if value := strings.TrimSpace(os.Getenv("BKN_TRACE_INTERNAL_HTTP_ADDRESS")); value != "" {
		config.InternalAddress = value
	}
	return config
}
