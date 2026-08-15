package sessionstore

import (
	v013 "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/migrations/mariadb/v013"
	v014 "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/migrations/mariadb/v014"
	v015 "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/migrations/mariadb/v015"
	v016 "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/migrations/mariadb/v016"
)

func SchemaSQL() string {
	return v013.SchemaSQL() + "\n" + v014.SchemaSQL() + "\n" + v015.SchemaSQL() + "\n" + v016.SchemaSQL()
}
