// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package conf

type HTTPServerConfig struct {
	Address         string
	InternalAddress string
}

func NewHTTPServerConfig() HTTPServerConfig {
	return HTTPServerConfig{
		Address:         ":8080",
		InternalAddress: ":8081",
	}
}
