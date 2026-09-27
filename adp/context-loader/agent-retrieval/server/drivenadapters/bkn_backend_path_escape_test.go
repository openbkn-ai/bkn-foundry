// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"
)

// Ids reach these endpoints from callers. One written raw with a % or a control
// character made url.Parse fail inside this process, which surfaced as an error
// with no status code instead of the service's answer about the id.
func TestBatchLookupsEscapeCallerIDsInThePath(t *testing.T) {
	ids := []string{"100%", "a b", "ot1"}
	for name, call := range map[string]func(*bknBackendAccess) error{
		"object types": func(c *bknBackendAccess) error {
			_, err := c.GetObjectTypeDetail(context.Background(), "kn%1", ids, true)
			return err
		},
		"relation types": func(c *bknBackendAccess) error {
			_, err := c.GetRelationTypeDetail(context.Background(), "kn%1", ids, true)
			return err
		},
		"action types": func(c *bknBackendAccess) error {
			_, err := c.GetActionTypeDetail(context.Background(), "kn%1", ids, true)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			client, mockHTTP, ctrl := newMetricsTestClient(t)
			defer ctrl.Finish()

			var got string
			mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, src string, _ url.Values, _ map[string]string) (int, []byte, error) {
					got = src
					return http.StatusOK, []byte(`{"entries":[]}`), nil
				})

			if err := call(client); err != nil {
				t.Fatalf("lookup failed: %v", err)
			}
			parsed, err := url.Parse(got)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", got, err)
			}
			wantNetwork := "/in/v1/knowledge-networks/kn%251/"
			if !strings.Contains(parsed.EscapedPath(), wantNetwork) {
				t.Fatalf("path %q does not carry the escaped network id %q", parsed.EscapedPath(), wantNetwork)
			}
			if tail := "/100%25,a%20b,ot1"; !strings.HasSuffix(parsed.EscapedPath(), tail) {
				t.Fatalf("path %q, want it to end in %q", parsed.EscapedPath(), tail)
			}
		})
	}
}
