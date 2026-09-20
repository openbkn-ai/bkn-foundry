// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestGiBToBytes(t *testing.T) {
	t.Run("converts GiB to bytes", func(t *testing.T) {
		assert.Equal(t, int64(0), GiBToBytes(0))
		assert.Equal(t, int64(1073741824), GiBToBytes(1))
		assert.Equal(t, int64(3221225472), GiBToBytes(3))
	})
}

func TestGetQueryOrDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name         string
		target       string
		key          string
		defaultValue string
		want         string
	}{
		{
			name:         "returns query value when key exists",
			target:       "/resources?limit=20",
			key:          "limit",
			defaultValue: "10",
			want:         "20",
		},
		{
			name:         "returns default when key is missing",
			target:       "/resources",
			key:          "limit",
			defaultValue: "10",
			want:         "10",
		},
		{
			name:         "returns default when query value is empty",
			target:       "/resources?limit=",
			key:          "limit",
			defaultValue: "10",
			want:         "10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", tt.target, nil)

			assert.Equal(t, tt.want, GetQueryOrDefault(c, tt.key, tt.defaultValue))
		})
	}
}

func TestEscapeLikePattern(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "keeps normal text unchanged",
			input: "customer",
			want:  "customer",
		},
		{
			name:  "escapes percent wildcard",
			input: "50% off",
			want:  `50\% off`,
		},
		{
			name:  "escapes underscore wildcard",
			input: "user_name",
			want:  `user\_name`,
		},
		{
			name:  "escapes existing backslash before wildcards",
			input: `path\_%`,
			want:  `path\\\_\%`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, EscapeLikePattern(tt.input))
		})
	}
}
