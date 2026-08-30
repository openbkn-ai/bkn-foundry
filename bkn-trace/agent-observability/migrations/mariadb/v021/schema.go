// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package v021

import _ "embed"

//go:embed init.sql
var schemaSQL string

func SchemaSQL() string { return schemaSQL }
