// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchprojection_test

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

func TestSinkClassifiesHTTPFailuresWithoutExposingResponse(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 408, 409, 425, 429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte("private response body"))
			}))
			defer server.Close()
			err := opensearchprojection.New(opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second), "core").Project(context.Background(), iprojectionoutbox.Item{AggregateVersion: 1})
			if status == 409 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var dependency *iprojectionoutbox.DependencyError
			if !errors.As(err, &dependency) || dependency.HTTPStatus != status || dependency.Recoverable != (status == 408 || status == 425 || status == 429 || status >= 500) || iprojectionoutbox.IsPermanent(err) != (status < 500 && status != 408 && status != 425 && status != 429) || strings.Contains(err.Error(), "private") {
				t.Fatalf("wrong classification: status=%d dependency=%+v err=%v", status, dependency, err)
			}
		})
	}
}
func TestSinkClassifiesTransportFailuresConservatively(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cause       error
		recoverable bool
	}{
		{"connection", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("private connection")}, true},
		{"dns", &net.DNSError{Err: "private DNS", Name: "secret"}, true},
		{"timeout", context.DeadlineExceeded, true},
		{"response truncated", io.ErrUnexpectedEOF, true},
		{"certificate", x509.UnknownAuthorityError{}, false},
		{"unknown", errors.New("private unknown"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := opensearch.NewWithHTTPClient("http://example.invalid", opensearch.AuthConfig{}, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, tc.cause })})
			err := opensearchprojection.New(client, "core").Project(context.Background(), iprojectionoutbox.Item{AggregateVersion: 1})
			var dependency *iprojectionoutbox.DependencyError
			if !errors.As(err, &dependency) || dependency.Recoverable != tc.recoverable || !errors.Is(err, tc.cause) || strings.Contains(err.Error(), "private") {
				t.Fatalf("classification=%+v err=%v", dependency, err)
			}
		})
	}
}
