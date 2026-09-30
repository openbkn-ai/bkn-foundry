// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.

package common

import (
	"errors"
	"fmt"
	"testing"
)

type fieldDatabaseError struct {
	code       string
	constraint string
}

func (err *fieldDatabaseError) Error() string { return "database error" }
func (err *fieldDatabaseError) Get(field byte) string {
	if field == 'C' {
		return err.code
	}
	if field == 'n' {
		return err.constraint
	}
	return ""
}

func TestDatabaseUniqueConstraint(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		constraint string
		unique     bool
	}{
		{name: "mysql name constraint", err: errors.New("Error 1062 (23000): Duplicate entry 'x' for key 'uk_object_type_name'"), constraint: "uk_object_type_name", unique: true},
		{name: "mysql qualified constraint", err: errors.New("Error 1062 (23000): Duplicate entry 'x' for key 'db.PRIMARY'"), constraint: "primary", unique: true},
		{name: "kingbase unique", err: &fieldDatabaseError{code: "23505", constraint: "uk_metric_name"}, constraint: "uk_metric_name", unique: true},
		{name: "wrapped kingbase unique", err: fmt.Errorf("insert: %w", &fieldDatabaseError{code: "23505", constraint: "PRIMARY"}), constraint: "primary", unique: true},
		{name: "other database error", err: errors.New("connection closed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			constraint, unique := DatabaseUniqueConstraint(test.err)
			if constraint != test.constraint || unique != test.unique {
				t.Fatalf("DatabaseUniqueConstraint() = (%q, %t), want (%q, %t)",
					constraint, unique, test.constraint, test.unique)
			}
		})
	}
}
