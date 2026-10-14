// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package mariadb provides MariaDB database connector implementation.
package mariadb

import (
	"testing"

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
