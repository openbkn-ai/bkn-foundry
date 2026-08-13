// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"vega-backend/interfaces"
)

func Test_isValidBuildTaskOrderBy(t *testing.T) {
	tests := []struct {
		orderBy string
		want    bool
	}{
		{orderBy: "default", want: false},
		{orderBy: interfaces.BuildTaskOrderByCreatedAt, want: true},
		{orderBy: interfaces.BuildTaskOrderByUpdatedAt, want: true},
		{orderBy: "status", want: false},
		{orderBy: "mode", want: false},
		{orderBy: "progress", want: false},
		{orderBy: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.orderBy, func(t *testing.T) {
			assert.Equal(t, tt.want, isValidBuildTaskOrderBy(tt.orderBy))
		})
	}
}
