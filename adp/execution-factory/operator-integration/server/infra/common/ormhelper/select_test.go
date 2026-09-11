package ormhelper

import (
	"reflect"
	"testing"
)

func TestSelectBuilderCompositeCursor(t *testing.T) {
	tests := []struct {
		name      string
		direction SortOrder
		operator  string
	}{
		{name: "ascending", direction: SortOrderAsc, operator: ">"},
		{name: "descending", direction: SortOrderDesc, operator: "<"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query, args := New(nil, "test").Select().From("skills").
				WhereEq("f_status", "published").
				Cursor(&CursorParams{
					Field:           "f_update_time",
					Value:           int64(100),
					TieBreakerField: "f_skill_id",
					TieBreakerValue: "skill-4999",
					Direction:       test.direction,
				}).
				Sort(&SortParams{Fields: []SortField{
					{Field: "f_update_time", Order: test.direction},
					{Field: "f_skill_id", Order: test.direction},
				}}).
				Build()

			expectedQuery := "SELECT * FROM `test`.`skills` WHERE f_status = ? AND " +
				"(f_update_time " + test.operator + " ? OR (f_update_time = ? AND f_skill_id " + test.operator + " ?))" +
				" ORDER BY f_update_time " + test.direction.String() + ", f_skill_id " + test.direction.String()
			if query != expectedQuery {
				t.Fatalf("unexpected query:\nwant: %s\n got: %s", expectedQuery, query)
			}
			expectedArgs := []interface{}{"published", int64(100), int64(100), "skill-4999"}
			if !reflect.DeepEqual(args, expectedArgs) {
				t.Fatalf("unexpected args: want %#v, got %#v", expectedArgs, args)
			}
		})
	}
}
