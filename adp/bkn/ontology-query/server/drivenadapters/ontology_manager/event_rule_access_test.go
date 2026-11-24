// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package ontology_manager

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	rmock "github.com/openbkn-ai/bkn-foundry/comm-go/rest/mock"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

func eventRuleClient(t *testing.T) (*eventRuleAccessClient, *rmock.MockHTTPClient) {
	t.Helper()
	client := rmock.NewMockHTTPClient(gomock.NewController(t))
	return &eventRuleAccessClient{bknBackendURL: "http://bkn-backend/api/bkn-backend/in/v1/knowledge-networks", httpClient: client}, client
}

func asSubject() context.Context {
	return context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "u-subject", Type: "user"})
}

func TestRecordRuleCheckNamesItsExecution(t *testing.T) {
	access, client := eventRuleClient(t)
	client.EXPECT().PostNoUnmarshal(gomock.Any(), "http://bkn-backend/api/bkn-backend/in/v1/knowledge-networks/kn-1/rule-checks",
		gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ string, headers map[string]string, body any) (int, []byte, error) {
		if headers[interfaces.HTTP_HEADER_ACCOUNT_ID] != "u-subject" || headers[interfaces.HTTP_HEADER_ACCOUNT_TYPE] != "user" ||
			headers["x-bkn-execution-id"] != "019a0000-0000-7000-8000-000000000001" {
			t.Errorf("headers = %v", headers)
		}
		encoded := string(body.([]byte))
		for _, want := range []string{`"event_type":"po_overdue"`, `"action_type_id":"po_overdue_recognize"`, `"rule_version":"a1b2c3"`,
			`"status":"ok"`, `"covered":null`, `"key":"9007199254740993"`} {
			if !strings.Contains(encoded, want) {
				t.Errorf("body %s lacks %s", encoded, want)
			}
		}
		if strings.Contains(encoded, `"detail"`) || strings.Contains(encoded, `"message"`) {
			t.Errorf("nothing to say is left out: %s", encoded)
		}
		return http.StatusOK, []byte(`{"events":[{"id":"ev-1"}],"check":{"opened":1}}`), nil
	})
	result, err := access.RecordRuleCheck(asSubject(), "kn-1", "019a0000-0000-7000-8000-000000000001", &interfaces.EventRuleCheckRequest{
		EventType: "po_overdue", ActionTypeID: "po_overdue_recognize", RuleVersion: "a1b2c3", Status: "ok",
		Hits: []interfaces.EventHit{{Key: "9007199254740993", Name: "PO", Attributes: map[string]interfaces.EventAttribute{}}},
	})
	if err != nil || len(result.Events) != 1 || result.Check["opened"] == nil {
		t.Fatalf("result = %+v, %v", result, err)
	}
}

func TestRecordRuleCheckKeepsTheLedgersRefusal(t *testing.T) {
	access, client := eventRuleClient(t)
	body, _ := json.Marshal(httperrors.BaseError{ErrorCode: "BknBackend.KnEvent.RecognitionMismatch",
		Description: "recognized by another action type", ErrorDetails: "po_overdue_v2"})
	client.EXPECT().PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(http.StatusConflict, body, nil)
	_, err := access.RecordRuleCheck(asSubject(), "kn-1", "e", &interfaces.EventRuleCheckRequest{})
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusConflict || httpErr.ErrorCode != "BknBackend.KnEvent.RecognitionMismatch" {
		t.Fatalf("err = %v", err)
	}

	client.EXPECT().PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(http.StatusBadGateway, []byte("bad gateway"), nil)
	_, err = access.RecordRuleCheck(asSubject(), "kn-1", "e", &interfaces.EventRuleCheckRequest{})
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadGateway || httpErr.ErrorDetails != "bad gateway" {
		t.Fatalf("a body that is not an error keeps its status and text: %v", err)
	}
}

func TestQueryCypherReadsTheBranch(t *testing.T) {
	access, client := eventRuleClient(t)
	client.EXPECT().PostNoUnmarshal(gomock.Any(), "http://bkn-backend/api/bkn-backend/in/v1/knowledge-networks/kn-1/cypher-queries?branch=feature%2Fa",
		gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ string, _ map[string]string, body any) (int, []byte, error) {
		if string(body.([]byte)) != `{"query":"MATCH (o) RETURN o","parameters":{}}` {
			t.Errorf("body = %s", body)
		}
		return http.StatusOK, []byte(`{"columns":[{"name":"key"}],"entries":[{"key":9007199254740993}],"limited":true}`), nil
	})
	result, err := access.QueryCypher(asSubject(), "kn-1", "feature/a", &interfaces.CypherQueryRequest{Query: "MATCH (o) RETURN o"})
	if err != nil || !result.Limited || result.Entries[0]["key"] != json.Number("9007199254740993") {
		t.Fatalf("result = %+v, %v", result, err)
	}
}
