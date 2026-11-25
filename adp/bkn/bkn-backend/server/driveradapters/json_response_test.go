// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"encoding/json/jsontext"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func TestCypherPlanUsesJSONV2WithOptionalGraphFlag(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	rest.ReplyOK(c, http.StatusOK, interfaces.CypherPlan{Shape: "match"})

	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response status = %d, headers = %v", recorder.Code, recorder.Header())
	}
	var fields map[string]jsontext.Value
	if err := jsoncodec.Unmarshal(recorder.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["graph"]; ok {
		t.Errorf("graph must be omitted when false, got %s", fields["graph"])
	}
	if string(fields["objects"]) != "[]" {
		t.Errorf("objects = %s, want an empty array", fields["objects"])
	}
}
