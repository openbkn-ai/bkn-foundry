// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package v024

import _ "embed"

//go:embed init.sql
var schemaSQL string

func SchemaSQL() string { return schemaSQL }
