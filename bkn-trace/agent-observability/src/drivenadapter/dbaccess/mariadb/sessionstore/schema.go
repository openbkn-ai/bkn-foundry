package sessionstore

import (
	v013 "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/migrations/mariadb/v013"
	v014 "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/migrations/mariadb/v014"
)

func SchemaSQL() string { return v013.SchemaSQL() + "\n" + v014.SchemaSQL() }
