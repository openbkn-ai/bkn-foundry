// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package rest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

type httpClientTestTransport func(*http.Request) (*http.Response, error)

func (transport httpClientTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func TestHTTPClientKeepsSandboxDefaultsAndPreciseResponse(t *testing.T) {
	client := &httpClient{
		logger: logger.DefaultLogger(),
		client: &http.Client{Transport: httpClientTestTransport(func(req *http.Request) (*http.Response, error) {
			defer func() { _ = req.Body.Close() }()
			var fields map[string]json.RawMessage
			if err := json.NewDecoder(req.Body).Decode(&fields); err != nil {
				return nil, err
			}
			for _, name := range []string{"install_timeout", "fail_on_dependency_error", "allow_version_conflicts"} {
				if _, ok := fields[name]; ok {
					return nil, fmt.Errorf("unexpected override of sandbox default %s", name)
				}
			}
			if string(fields["id"]) != `"session-1"` || string(fields["timeout"]) != "300" {
				return nil, fmt.Errorf("session request lost its ID or timeout: %v", fields)
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(strings.NewReader(`{"offset":9007199254740993}`)),
			}, nil
		})},
	}

	code, response, err := client.Post(t.Context(), "http://sandbox.test/sessions", nil, &interfaces.CreateSessionReq{
		ID: "session-1", TemplateID: "template-1", Timeout: 300,
	})
	if err != nil || code != http.StatusCreated {
		t.Fatalf("create session: status=%d, response=%v, err=%v", code, response, err)
	}
	fields, ok := response.(map[string]any)
	if !ok || fields["offset"] != json.Number("9007199254740993") {
		t.Fatalf("response lost numeric precision: %#v", response)
	}
}

func TestHTTPClientPostStreamPreservesRequestAndEvent(t *testing.T) {
	const event = `data: {"id":9007199254740993}`
	client := &httpClient{client: &http.Client{Transport: httpClientTestTransport(func(req *http.Request) (*http.Response, error) {
		defer func() { _ = req.Body.Close() }()
		var body interfaces.ChatCompletionReq
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}
		if req.Header.Get("Accept") != "text/event-stream" || !body.Stream || body.Model != "demo" {
			return nil, fmt.Errorf("unexpected streaming request: accept=%q, stream=%t, model=%q",
				req.Header.Get("Accept"), body.Stream, body.Model)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(event + "\n\n"))}, nil
	})}}

	messages, errs, err := client.PostStream(t.Context(), "http://model.test/chat/completions", nil, &interfaces.ChatCompletionReq{
		Model: "demo", Stream: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var received []string
	for messages != nil || errs != nil {
		select {
		case message, ok := <-messages:
			if ok {
				received = append(received, message)
			} else {
				messages = nil
			}
		case err, ok := <-errs:
			if ok {
				t.Fatal(err)
			}
			errs = nil
		}
	}
	if len(received) != 1 || received[0] != event {
		t.Fatalf("streamed events = %q, want %q", received, event)
	}
}
