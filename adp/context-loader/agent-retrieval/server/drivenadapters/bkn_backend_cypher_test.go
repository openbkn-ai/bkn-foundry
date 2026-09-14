// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"go.uber.org/mock/gomock"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

// #1545: bkn-backend refuses a Cypher query with 403 and a code of its own --
// a property the caller may not read in full, or a resource the data source
// refused. Both reach the caller with the same status and code, not as a
// gateway failure and not as a failed authentication of Context Loader itself.
func TestRunCypherQueryPassesForbiddenThrough(t *testing.T) {
	for _, code := range []string{"BknBackend.Cypher.PropertyForbidden", "BknBackend.Cypher.Forbidden"} {
		t.Run(code, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockLogger := mocks.NewMockLogger(ctrl)
			mockHTTPClient := mocks.NewMockHTTPClient(ctrl)
			mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
			mockHTTPClient.EXPECT().
				PostNoUnmarshal(gomock.Any(), "http://bkn/in/v1/knowledge-networks/kn-1/cypher-queries", gomock.Any(), gomock.Any()).
				Return(http.StatusForbidden, []byte(`{"error_code":"`+code+`","description":"refused",`+
					`"solution":"ask","error_details":"ot_user.phone"}`), nil)

			client := &bknBackendAccess{logger: mockLogger, baseURL: "http://bkn", httpClient: mockHTTPClient}
			_, err := client.RunCypherQuery(context.Background(), &interfaces.CypherQueryReq{
				KnID: "kn-1", Query: "MATCH (u:user) RETURN u.phone",
			})

			var httpErr *infraErr.HTTPError
			if !errors.As(err, &httpErr) {
				t.Fatalf("RunCypherQuery() error = %T %v, want *HTTPError", err, err)
			}
			if httpErr.HTTPCode != http.StatusForbidden || httpErr.Code != code {
				t.Fatalf("RunCypherQuery() error = %d %s, want 403 %s", httpErr.HTTPCode, httpErr.Code, code)
			}
			if httpErr.ErrorDetails != "ot_user.phone" {
				t.Fatalf("details = %v, want the property the refusal names", httpErr.ErrorDetails)
			}
		})
	}
}
