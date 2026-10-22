// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mariadb

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

func TestSQLEmitterRelation(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		args []any
	}{
		{sql: "SELECT 'outer' AS label, a.id FROM (SELECT id FROM {{ orders }} WHERE id>2 ORDER BY id LIMIT 5) a", args: []any{"outer", int64(2), int64(5), int64(0)}},
		{sql: "SELECT id FROM {{ orders }} WHERE name IN ('a', 'a ') ORDER BY name DESC LIMIT 2", args: []any{"a", "a ", int64(2), int64(0)}},
		{sql: "SELECT id FROM {{ orders }} WHERE id BETWEEN 1 AND 3 AND name IS NOT NULL", args: []any{int64(1), int64(3)}},
		{sql: "SELECT id FROM {{ orders }} WHERE id IN (1, NULL)", args: []any{int64(1), sql.NullInt64{}}},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			req, _ := sqlTestPlan(t, tc.sql)
			emitter := sqlEmitter{ctx: context.Background(), target: req.TargetBinding}
			relation, err := emitter.relation(req.Query.Root, 1)
			require.NoError(t, err)
			assert.Equal(t, tc.args, relation.args)
			assert.Equal(t, len(relation.args), strings.Count(relation.text, "?"))
			assert.Equal(t, req.Query.OutputSchema, relation.schema)
		})
	}
	t.Run("cyclic relation bounded before SQL", func(t *testing.T) {
		root := &qm.BoundFilter{}
		root.Input = root
		emitter := sqlEmitter{ctx: context.Background()}
		_, err := emitter.relation(root, 1)
		assert.Error(t, err)
	})
}
