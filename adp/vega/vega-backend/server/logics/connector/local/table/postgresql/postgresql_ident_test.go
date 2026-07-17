// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"vega-backend/interfaces"
)

func TestPgQuoteIdent(t *testing.T) {
	t.Run("pg quote ident", func(t *testing.T) {
		assert.Equal(t, `""`, pgQuoteIdent(""))
		assert.Equal(t, `"users"`, pgQuoteIdent("users"))
		assert.Equal(t, `"weird""name"`, pgQuoteIdent(`weird"name`))
	})
}

func TestQualTable(t *testing.T) {
	t.Run("qual table", func(t *testing.T) {
		assert.Equal(t, `"users"`, qualTable(&interfaces.Resource{SourceIdentifier: "users"}))
		assert.Equal(t, `"public"."users"`, qualTable(&interfaces.Resource{SourceIdentifier: " public . users "}))
		assert.Equal(t, `"db"."public.users"`, qualTable(&interfaces.Resource{SourceIdentifier: "db.public.users"}))
	})
}

func TestQuoteColumnName(t *testing.T) {
	t.Run("quote column name", func(t *testing.T) {
		assert.Equal(t, `""`, quoteColumnName(""))
		assert.Equal(t, `"id"`, quoteColumnName(" id "))
		assert.Equal(t, `"u"."id"`, quoteColumnName(" u . id "))
		assert.Equal(t, `"u"."first.name"`, quoteColumnName("u.first.name"))
	})
}
