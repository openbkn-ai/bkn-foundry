// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

type overviewGraphBranchStub struct {
	interfaces.KNService
	branch string
	calls  int
}

func (s *overviewGraphBranchStub) ListOverviewGraph(_ context.Context, _ string, query interfaces.OverviewGraphQuery) (*interfaces.OverviewGraph, error) {
	s.branch = query.Branch
	s.calls++
	return &interfaces.OverviewGraph{}, nil
}

// The overview graph reads the network version kn_version selects, as every definition read
// does: on the draft it draws the draft's object and relation types, such as an event class
// that is not published yet, not the running version's.
func TestListOverviewGraphReadsTheSelectedNetworkVersion(t *testing.T) {
	restoreGin := setGinMode()
	defer restoreGin()
	for query, want := range map[string]string{
		"":                         "v2",
		"kn_version=running":       "v2",
		"kn_version=draft":         interfaces.DRAFT_BRANCH,
		"kn_version=1":             "v1",
		"kn_version=not_a_version": "",
	} {
		t.Run(query, func(t *testing.T) {
			service := &overviewGraphBranchStub{}
			handler := &restHandler{kns: service, kvs: metricNetworkVersions{}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet,
				"/api/bkn-backend/v1/knowledge-networks/kn-a/overview-graph?"+query, nil)
			c.Params = gin.Params{{Key: "kn_id", Value: "kn-a"}}

			handler.ListOverviewGraph(c, hydra.Visitor{})

			if want == "" {
				if recorder.Code != http.StatusBadRequest || service.calls != 0 ||
					!strings.Contains(recorder.Body.String(), berrors.BknBackend_KNVersion_InvalidVersion) {
					t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.calls, recorder.Body.String())
				}
				return
			}
			if recorder.Code != http.StatusOK || service.branch != want {
				t.Fatalf("status=%d branch=%q want %q body=%s", recorder.Code, service.branch, want, recorder.Body.String())
			}
		})
	}
}
