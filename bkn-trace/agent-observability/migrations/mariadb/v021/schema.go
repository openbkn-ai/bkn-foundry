// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package v021

import _ "embed"

//go:embed init.sql
var schemaSQL string

func SchemaSQL() string { return schemaSQL }
