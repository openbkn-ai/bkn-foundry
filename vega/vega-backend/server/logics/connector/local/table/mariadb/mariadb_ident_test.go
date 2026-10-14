// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mariadb

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMariaDBQuoteIdent(t *testing.T) {
	for _, tt := range []struct{ name, want string }{
		{"", "``"},
		{"orders", "`orders`"},
		{"ord`ers", "`ord``ers`"},
		{"orders.part", "`orders.part`"},
		{" orders ", "` orders `"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, mariadbQuoteIdent(tt.name))
		})
	}
}

func TestQuoteColumnName(t *testing.T) {
	t.Run("quote column name simple", func(t *testing.T) {
		if got := mariadbQuoteColumnName("name"); got != "`name`" {
			t.Errorf("expected `name`, got %s", got)
		}
	})
	t.Run("quote column name with alias", func(t *testing.T) {
		got := mariadbQuoteColumnName("t1.name")
		if got != "`t1`.`name`" {
			t.Errorf("expected `t1`.`name`, got %s", got)
		}
	})
	t.Run("quote column name empty", func(t *testing.T) {
		if got := mariadbQuoteColumnName(""); got != "``" {
			t.Errorf("expected ``, got %s", got)
		}
	})
	t.Run("quote column name with backtick", func(t *testing.T) {
		got := mariadbQuoteColumnName("col`name")
		if got != "`col``name`" {
			t.Errorf("expected `col``name`, got %s", got)
		}
	})
	t.Run("quote column name alias with spaces", func(t *testing.T) {
		got := mariadbQuoteColumnName(" t1 . name ")
		if got != "`t1`.`name`" {
			t.Errorf("expected `t1`.`name`, got %s", got)
		}
	})
}

func TestMariaDBConnectorBuildSourceIdentifier(t *testing.T) {
	for _, tc := range []struct{ name, namespace, table, want string }{
		{"ordinary", "sales", "orders", "`sales`.`orders`"},
		{"dot in component", "sales.eu", "orders.part", "`sales.eu`.`orders.part`"},
		{"spaces", " sales ", " orders ", "` sales `.` orders `"},
		{"reserved word", "schema", "select", "`schema`.`select`"},
		{"case", "Sales", "Orders", "`Sales`.`Orders`"},
		{"quote", "sch`ema", "ord`ers", "`sch``ema`.`ord``ers`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := &interfaces.TableMeta{Database: tc.namespace, Name: tc.table}
			reference := (&MariaDBConnector{}).BuildSourceIdentifier(meta)
			assert.Equal(t, tc.want, reference)
			assert.Equal(t, tc.namespace, meta.Database)
			assert.Equal(t, tc.table, meta.Name)
		})
	}

	t.Run("does not mutate metadata or connect", func(t *testing.T) {
		connector := &MariaDBConnector{}
		meta := &interfaces.TableMeta{Database: "sales", Name: "order.items"}
		before := *meta
		assert.Equal(t, "`sales`.`order.items`", connector.BuildSourceIdentifier(meta))
		assert.Equal(t, connector.BuildSourceIdentifier(meta), connector.BuildSourceIdentifier(meta))
		assert.False(t, connector.connected)
		assert.Equal(t, before, *meta)
	})
}

func TestMariaDBConnectorBuildOriginalName(t *testing.T) {
	connector := &MariaDBConnector{}
	for _, tc := range []struct {
		name string
		meta *interfaces.TableMeta
		want string
	}{
		{"database preferred", &interfaces.TableMeta{Database: "app", Schema: "sales", Name: "orders"}, "app.orders"},
		{"schema fallback", &interfaces.TableMeta{Schema: "app", Name: "orders"}, "app.orders"},
		{"raw components", &interfaces.TableMeta{Database: "app`db", Name: "order.items"}, "app`db.order.items"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := connector.BuildOriginalName(tc.meta)
			assert.Equal(t, tc.want, got)
		})
	}
}
