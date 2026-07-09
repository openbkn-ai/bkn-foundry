// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

// newListCtx 造一个仅带 query 的 GET 测试上下文。
func newListCtx(query string) *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/build-tasks?"+query, nil)
	return c
}

func Test_parseBuildTaskStatuses(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{
			name: "single valid",
			raw:  "running",
			want: []string{"running"},
		},
		{
			name: "multi valid with spaces",
			raw:  "running, init",
			want: []string{"running", "init"},
		},
		{
			name:    "one invalid value returns error",
			raw:     "running,unknown",
			wantErr: true,
		},
		{
			name: "only empty segments returns empty slice",
			raw:  ", , ",
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseBuildTaskStatuses(ctx, tt.raw)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_isValidBuildTaskOrderBy(t *testing.T) {
	tests := []struct {
		orderBy string
		want    bool
	}{
		{orderBy: interfaces.BuildTaskOrderByDefault, want: true},
		{orderBy: interfaces.BuildTaskOrderByCreatedAt, want: true},
		{orderBy: interfaces.BuildTaskOrderByUpdatedAt, want: true},
		{orderBy: interfaces.BuildTaskOrderByStatus, want: true},
		{orderBy: interfaces.BuildTaskOrderByMode, want: true},
		{orderBy: "progress", want: false},
		{orderBy: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.orderBy, func(t *testing.T) {
			assert.Equal(t, tt.want, isValidBuildTaskOrderBy(tt.orderBy))
		})
	}
}

func Test_parseBuildTaskListParams(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		query   string
		assert  func(t *testing.T, got interfaces.BuildTasksQueryParams)
		wantErr bool
	}{
		{
			name:  "defaults when no query",
			query: "",
			assert: func(t *testing.T, got interfaces.BuildTasksQueryParams) {
				assert.Equal(t, 0, got.Offset)
				assert.Equal(t, 20, got.Limit)
				assert.Equal(t, interfaces.BuildTaskOrderByDefault, got.OrderBy)
				assert.Equal(t, interfaces.DESC_DIRECTION, got.Order)
				assert.Empty(t, got.Statuses)
			},
		},
		{
			name:  "active true overrides status with running and init",
			query: "active=true&status=completed",
			assert: func(t *testing.T, got interfaces.BuildTasksQueryParams) {
				assert.Equal(t, []string{interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusInit}, got.Statuses)
			},
		},
		{
			name:  "multi-value status",
			query: "status=running,init",
			assert: func(t *testing.T, got interfaces.BuildTasksQueryParams) {
				assert.Equal(t, []string{"running", "init"}, got.Statuses)
			},
		},
		{
			name:  "order by and order honored",
			query: "order_by=created_at&order=asc",
			assert: func(t *testing.T, got interfaces.BuildTasksQueryParams) {
				assert.Equal(t, interfaces.BuildTaskOrderByCreatedAt, got.OrderBy)
				assert.Equal(t, interfaces.ASC_DIRECTION, got.Order)
			},
		},
		{
			name:    "invalid order by returns error",
			query:   "order_by=bogus",
			wantErr: true,
		},
		{
			name:    "invalid order returns error",
			query:   "order=sideways",
			wantErr: true,
		},
		{
			name:    "invalid status returns error",
			query:   "status=running,nope",
			wantErr: true,
		},
		{
			name:    "invalid mode returns error",
			query:   "mode=nope",
			wantErr: true,
		},
		{
			name:    "negative offset returns error",
			query:   "offset=-1",
			wantErr: true,
		},
		{
			name:  "limit no-limit allowed",
			query: "limit=-1",
			assert: func(t *testing.T, got interfaces.BuildTasksQueryParams) {
				assert.Equal(t, -1, got.Limit)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseBuildTaskListParams(ctx, newListCtx(tt.query))

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.assert != nil {
				tt.assert(t, got)
			}
		})
	}
}
