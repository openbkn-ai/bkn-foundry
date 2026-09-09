// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.uber.org/mock/gomock"

	"bkn-backend/common"
	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

const cypherKNID = "kn_1"

func cypherTestHandler(t *testing.T) (*gin.Engine, *bmock.MockCypherQueryService, *bmock.MockKNService) {
	t.Helper()

	restore := setGinMode()
	t.Cleanup(restore)

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	auth := bmock.NewMockAuthService(ctrl)
	auth.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().Return(hydra.Visitor{ID: "user-42", Type: "user"}, nil)

	knowledgeNetworks := bmock.NewMockKNService(ctrl)
	cypherQueries := bmock.NewMockCypherQueryService(ctrl)

	handler := &restHandler{
		appSetting: &common.AppSetting{},
		as:         auth,
		kns:        knowledgeNetworks,
		cqs:        cypherQueries,
	}
	engine := gin.New()
	engine.Use(gin.Recovery())
	handler.RegisterPublic(engine)

	return engine, cypherQueries, knowledgeNetworks
}

func postCypher(engine *gin.Engine, url, body string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(body)))
	request.Header.Set(interfaces.CONTENT_TYPE_NAME, interfaces.CONTENT_TYPE_JSON)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func cypherURL(face, knID string) string {
	return "/api/bkn-backend/" + face + "v1/knowledge-networks/" + knID + "/cypher-queries"
}

// Both faces reach the same handler. The internal one takes the caller from
// headers, which is what the services below authorize against.
func TestRunCypherQueryOnBothFaces(t *testing.T) {
	for _, face := range []string{"", "in/"} {
		t.Run("face "+face, func(t *testing.T) {
			engine, cypherQueries, knowledgeNetworks := cypherTestHandler(t)
			knowledgeNetworks.EXPECT().CheckKNExistByID(gomock.Any(), cypherKNID, interfaces.MAIN_BRANCH).
				Return(cypherKNID, true, nil)
			cypherQueries.EXPECT().
				Query(gomock.Any(), interfaces.CypherQuery{
					KNID: cypherKNID, Branch: interfaces.MAIN_BRANCH, Query: "MATCH (o:Order) RETURN o.id",
				}).
				Return(&interfaces.CypherQueryResult{
					Columns: []interfaces.RawQueryColumn{{Name: "id", Type: "string"}},
					Entries: []map[string]any{{"id": "1"}},
				}, nil)

			recorder := postCypher(engine, cypherURL(face, cypherKNID),
				`{"query":"MATCH (o:Order) RETURN o.id"}`,
				map[string]string{
					interfaces.HTTP_HEADER_ACCOUNT_ID:   "user-42",
					interfaces.HTTP_HEADER_ACCOUNT_TYPE: "user",
				})

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			for _, want := range []string{`"id"`, `"entries"`, `"columns"`} {
				if !bytes.Contains(recorder.Body.Bytes(), []byte(want)) {
					t.Fatalf("body = %s, want it to contain %s", recorder.Body.String(), want)
				}
			}
		})
	}
}

// The branch is part of what a query is compiled against, so it has to reach
// the service rather than being defaulted twice.
func TestRunCypherQueryPassesTheBranch(t *testing.T) {
	engine, cypherQueries, knowledgeNetworks := cypherTestHandler(t)
	knowledgeNetworks.EXPECT().CheckKNExistByID(gomock.Any(), cypherKNID, "release").
		Return(cypherKNID, true, nil)
	cypherQueries.EXPECT().
		Query(gomock.Any(), interfaces.CypherQuery{
			KNID: cypherKNID, Branch: "release", Query: "MATCH (o:Order) RETURN o.id",
		}).
		Return(&interfaces.CypherQueryResult{}, nil)

	recorder := postCypher(engine, cypherURL("in/", cypherKNID)+"?branch=release",
		`{"query":"MATCH (o:Order) RETURN o.id"}`, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

// The caller's identity has to be in the context before the service runs:
// everything below authorizes against it.
func TestRunCypherQueryPutsTheCallerInContext(t *testing.T) {
	engine, cypherQueries, knowledgeNetworks := cypherTestHandler(t)
	knowledgeNetworks.EXPECT().CheckKNExistByID(gomock.Any(), cypherKNID, gomock.Any()).
		Return(cypherKNID, true, nil)
	cypherQueries.EXPECT().
		Query(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ interfaces.CypherQuery) (*interfaces.CypherQueryResult, error) {
			account, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
			if !ok || account.ID != "user-7" || account.Type != "user" {
				t.Fatalf("account in context = %v (ok=%v), want user-7", account, ok)
			}
			return &interfaces.CypherQueryResult{}, nil
		})

	recorder := postCypher(engine, cypherURL("in/", cypherKNID),
		`{"query":"MATCH (o:Order) RETURN o.id"}`,
		map[string]string{
			interfaces.HTTP_HEADER_ACCOUNT_ID:   "user-7",
			interfaces.HTTP_HEADER_ACCOUNT_TYPE: "user",
		})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestRunCypherQueryRejectsBadRequests(t *testing.T) {
	t.Run("body that is not JSON", func(t *testing.T) {
		engine, _, _ := cypherTestHandler(t)
		recorder := postCypher(engine, cypherURL("in/", cypherKNID), `{"query":`, nil)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", recorder.Code)
		}
	})

	t.Run("content type that is not JSON", func(t *testing.T) {
		engine, _, _ := cypherTestHandler(t)
		request := httptest.NewRequest(http.MethodPost, cypherURL("in/", cypherKNID),
			bytes.NewReader([]byte(`{"query":"MATCH (o:Order) RETURN o.id"}`)))
		request.Header.Set(interfaces.CONTENT_TYPE_NAME, "text/plain")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotAcceptable {
			t.Fatalf("status = %d, want 406", recorder.Code)
		}
	})

	t.Run("knowledge network that does not exist", func(t *testing.T) {
		engine, _, knowledgeNetworks := cypherTestHandler(t)
		knowledgeNetworks.EXPECT().CheckKNExistByID(gomock.Any(), "missing", gomock.Any()).
			Return("", false, nil)

		recorder := postCypher(engine, cypherURL("in/", "missing"),
			`{"query":"MATCH (o:Order) RETURN o.id"}`, nil)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", recorder.Code)
		}
	})
}

// The service reports refusals as HTTP errors, and the handler must carry the
// status it chose rather than flattening everything into 500.
func TestRunCypherQueryKeepsTheServiceStatus(t *testing.T) {
	engine, cypherQueries, knowledgeNetworks := cypherTestHandler(t)
	knowledgeNetworks.EXPECT().CheckKNExistByID(gomock.Any(), cypherKNID, gomock.Any()).
		Return(cypherKNID, true, nil)
	cypherQueries.EXPECT().Query(gomock.Any(), gomock.Any()).
		Return(nil, rest.NewHTTPError(context.Background(), http.StatusForbidden, rest.PublicError_Forbidden))

	recorder := postCypher(engine, cypherURL("in/", cypherKNID),
		`{"query":"MATCH (o:Order) RETURN o.id"}`, nil)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}

// A dependency that returns a plain error used to be type-asserted, which
// panicked. It becomes a 500, and the error text does not travel with it.
func TestRunCypherQuerySurvivesAPlainError(t *testing.T) {
	engine, cypherQueries, knowledgeNetworks := cypherTestHandler(t)
	knowledgeNetworks.EXPECT().CheckKNExistByID(gomock.Any(), cypherKNID, gomock.Any()).
		Return(cypherKNID, true, nil)
	cypherQueries.EXPECT().Query(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("connection refused to 10.0.0.1:3306"))

	recorder := postCypher(engine, cypherURL("in/", cypherKNID),
		`{"query":"MATCH (o:Order) RETURN o.id"}`, nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("10.0.0.1")) {
		t.Fatalf("the dependency's message reached the caller: %s", recorder.Body.String())
	}
}

// The existence check itself can fail, and that is not a missing network.
func TestRunCypherQueryReportsAFailedExistenceCheck(t *testing.T) {
	engine, _, knowledgeNetworks := cypherTestHandler(t)
	knowledgeNetworks.EXPECT().CheckKNExistByID(gomock.Any(), cypherKNID, gomock.Any()).
		Return("", false, errors.New("database is down"))

	recorder := postCypher(engine, cypherURL("in/", cypherKNID),
		`{"query":"MATCH (o:Order) RETURN o.id"}`, nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
}
