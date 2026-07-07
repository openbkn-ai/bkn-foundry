// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import "testing"

func TestPostgresqlTableTypeFromRelkind(t *testing.T) {
	tests := []struct {
		name    string
		relKind string
		want    string
	}{
		{name: "regular table", relKind: "r", want: "table"},
		{name: "partitioned table", relKind: "p", want: "table"},
		{name: "foreign table", relKind: "f", want: "table"},
		{name: "view", relKind: "v", want: "view"},
		{name: "materialized view", relKind: "m", want: "materialized_view"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := postgresqlTableTypeFromRelkind(tt.relKind); got != tt.want {
				t.Fatalf("expected %s, got %s", tt.want, got)
			}
		})
	}
}
