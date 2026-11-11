package drivenadapters

import (
	"fmt"
	"io"
	"net/http"
)

// mcpAuthBodyLimit caps how much of a rejection body goes into the error.
const mcpAuthBodyLimit = 512

// mcpAuthStatusClient returns a client that turns an upstream credential rejection (401, 403,
// 407) into a transport error worded as mcp-go words its own status errors.
//
// mcp-go v0.37.0 reads any non-2xx body that parses as JSON as a JSON-RPC response and returns it
// without an error. A server that rejects the credentials with a JSON error body of its own, as
// Context Loader does, then yields an empty response, the status is lost, and the handshake fails
// with "unexpected end of JSON input". classifyMCPConnError recognizes the wording below and
// reports a credential problem instead of an unreachable server.
func mcpAuthStatusClient(base *http.Client) *http.Client {
	wrapped := *base
	next := base.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	wrapped.Transport = mcpAuthStatusTransport{next: next}
	return &wrapped
}

type mcpAuthStatusTransport struct {
	next http.RoundTripper
}

func (t mcpAuthStatusTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusProxyAuthRequired:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, mcpAuthBodyLimit))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, body)
	}
	return resp, nil
}
