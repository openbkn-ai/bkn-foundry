// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/stretchr/testify/assert"
)

func TestPgQuoteIdent(t *testing.T) {
	t.Run("pg quote ident", func(t *testing.T) {
		assert.Equal(t, `""`, pgQuoteIdent(""))
		assert.Equal(t, `"users"`, pgQuoteIdent("users"))
		assert.Equal(t, `"weird""name"`, pgQuoteIdent(`weird"name`))
	})
}

func TestQuoteColumnName(t *testing.T) {
	t.Run("quote column name", func(t *testing.T) {
		assert.Equal(t, `""`, pgQuoteColumnName(""))
		assert.Equal(t, `"id"`, pgQuoteColumnName(" id "))
		assert.Equal(t, `"u"."id"`, pgQuoteColumnName(" u . id "))
		assert.Equal(t, `"u"."first.name"`, pgQuoteColumnName("u.first.name"))
	})
}

func TestPostgresqlConnectorBuildSourceIdentifier(t *testing.T) {
	for _, tc := range []struct{ name, namespace, table, want string }{
		{"ordinary", "sales", "orders", "\"sales\".\"orders\""},
		{"dot in component", "sales.eu", "orders.part", "\"sales.eu\".\"orders.part\""},
		{"spaces", " sales ", " orders ", "\" sales \".\" orders \""},
		{"reserved word", "schema", "select", "\"schema\".\"select\""},
		{"case", "Sales", "Orders", "\"Sales\".\"Orders\""},
		{"quote", "sch\"ema", "ord\"ers", "\"sch\"\"ema\".\"ord\"\"ers\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := &interfaces.TableMeta{Schema: tc.namespace, Name: tc.table}
			reference := (&PostgresqlConnector{}).BuildSourceIdentifier(meta)
			assert.Equal(t, tc.want, reference)
			assert.Equal(t, tc.namespace, meta.Schema)
			assert.Equal(t, tc.table, meta.Name)
		})
	}

	t.Run("does not mutate metadata or connect", func(t *testing.T) {
		connector := &PostgresqlConnector{}
		meta := &interfaces.TableMeta{Schema: "sales", Name: "order.items"}
		before := *meta
		assert.Equal(t, "\"sales\".\"order.items\"", connector.BuildSourceIdentifier(meta))
		assert.Equal(t, connector.BuildSourceIdentifier(meta), connector.BuildSourceIdentifier(meta))
		assert.False(t, connector.connected)
		assert.Equal(t, before, *meta)
	})
}

func TestPostgresqlConnectorBuildOriginalName(t *testing.T) {
	connector := &PostgresqlConnector{}
	for _, tc := range []struct {
		name string
		meta *interfaces.TableMeta
		want string
	}{
		{"schema qualified", &interfaces.TableMeta{Database: "app", Schema: "sales", Name: "orders"}, "sales.orders"},
		{"raw components", &interfaces.TableMeta{Schema: "sales space", Name: "order.items"}, "sales space.order.items"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := connector.BuildOriginalName(tc.meta)
			assert.Equal(t, tc.want, got)
		})
	}
}
