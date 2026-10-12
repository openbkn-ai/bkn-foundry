// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"testing"

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
