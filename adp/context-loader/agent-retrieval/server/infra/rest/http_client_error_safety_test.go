package rest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type capturedErrorLogger struct {
	interfaces.Logger
	messages []string
}

func (l *capturedErrorLogger) Errorf(format string, args ...interface{}) {
	l.messages = append(l.messages, fmt.Sprintf(format, args...))
}
func (l *capturedErrorLogger) Error(args ...interface{}) {
	l.messages = append(l.messages, fmt.Sprint(args...))
}

func TestDependencyErrorsKeepCredentialsAndRawBodiesOutOfLogs(t *testing.T) {
	for _, body := range []string{`{"code":"Public.UnprocessableEntity","details":"secret-response"}`, `secret-response invalid JSON`} {
		for _, bytesMode := range []bool{false, true} {
			t.Run(fmt.Sprintf("bytes=%v body=%s", bytesMode, body[:1]), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(422); _, _ = w.Write([]byte(body)) }))
				defer server.Close()
				logs := &capturedErrorLogger{}
				client := &httpClient{client: server.Client(), logger: logs}
				headers := map[string]string{"Authorization": "Bearer secret-token"}
				params := map[string]any{"code": "secret-source", "token": "secret-event"}
				var err error
				if bytesMode {
					_, _, err = client.PostBytes(context.Background(), server.URL, headers, params)
				} else {
					_, _, err = client.Post(context.Background(), server.URL, headers, params)
				}
				var he *infraErr.DependencyError
				if !errors.As(err, &he) || he.Err.HTTPCode != 422 || string(he.Body) != body {
					t.Fatalf("classification context lost: %v", err)
				}
				safeLogs := strings.Join(logs.messages, "\n")
				for _, leak := range []string{"secret-token", "secret-event", "secret-source", "secret-response", server.URL} {
					if strings.Contains(safeLogs, leak) {
						t.Fatalf("leaked %s: %s", leak, safeLogs)
					}
				}
				if len(logs.messages) == 0 || !strings.Contains(safeLogs, "422") {
					t.Fatalf("missing safe failure diagnosis: %s", safeLogs)
				}
			})
		}
	}
}
