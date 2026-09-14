// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"ontology-query/interfaces"
)

func TestActionLogQueryBindsKeyword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	// The Studio task panel sends keyword=<part of an execution id> (#790).
	c.Request = httptest.NewRequest("GET", "/action-logs?keyword=01a0%2A&limit=10&need_total=true", nil)

	query := interfaces.ActionLogQuery{}
	if err := c.ShouldBindQuery(&query); err != nil {
		t.Fatal(err)
	}
	if query.Keyword != "01a0*" {
		t.Fatalf("keyword was not bound from the query string: %q", query.Keyword)
	}
}

func TestNormalizeActionLogKeyword(t *testing.T) {
	got, err := normalizeActionLogKeyword("  01a0abc  ")
	if err != nil || got != "01a0abc" {
		t.Fatalf("expected trimmed keyword, got %q, %v", got, err)
	}

	if _, err := normalizeActionLogKeyword(strings.Repeat("é", maxActionLogKeywordLength)); err != nil {
		t.Fatalf("a keyword at the limit is accepted, counted in characters: %v", err)
	}

	if _, err := normalizeActionLogKeyword(strings.Repeat("a", maxActionLogKeywordLength+1)); err == nil {
		t.Fatal("an oversized keyword must be rejected")
	}
}
