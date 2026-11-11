// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDefaultBranchIsMain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(defaultBranchAlias())
	engine.GET("/kn", func(c *gin.Context) { c.String(http.StatusOK, c.Query("branch")+"|"+c.Query("limit")) })
	for query, want := range map[string]string{
		"?branch=default&limit=5": "main|5",
		"?branch=main":            "main|",
		"?branch=draft":           "draft|",
		"":                        "|",
	} {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/kn"+query, nil))
		if recorder.Body.String() != want {
			t.Fatalf("%q read as %q, want %q", query, recorder.Body.String(), want)
		}
	}
}
