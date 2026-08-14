package v016

import _ "embed"

//go:embed init.sql
var schemaSQL string

func SchemaSQL() string { return schemaSQL }
