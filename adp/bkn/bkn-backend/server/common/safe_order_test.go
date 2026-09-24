// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for license information.

package common

import "testing"

func TestSafeOrderBy(t *testing.T) {
	tests := []struct {
		name      string
		column    string
		direction string
		want      string
		wantErr   bool
	}{
		{name: "ascending name", column: "f_name", direction: "asc", want: "f_name ASC"},
		{name: "descending update time", column: "f_update_time", direction: "desc", want: "f_update_time DESC"},
		{name: "uppercase direction", column: "f_name", direction: "DESC", want: "f_name DESC"},
		{name: "schedule time", column: "f_next_run_time", direction: "asc", want: "f_next_run_time ASC"},
		{name: "reject injected column", column: "f_name; DROP TABLE t", direction: "asc", wantErr: true},
		{name: "reject injected direction", column: "f_name", direction: "desc; DROP TABLE t", wantErr: true},
		{name: "reject empty direction", column: "f_name", direction: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SafeOrderBy(tt.column, tt.direction)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("SafeOrderBy(%q, %q) expected an error", tt.column, tt.direction)
				}
				return
			}
			if err != nil {
				t.Fatalf("SafeOrderBy(%q, %q) returned error: %v", tt.column, tt.direction, err)
			}
			if got != tt.want {
				t.Fatalf("SafeOrderBy(%q, %q) = %q, want %q", tt.column, tt.direction, got, tt.want)
			}
		})
	}
}
