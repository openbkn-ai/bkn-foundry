// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package executiongrant issues and revokes, at bkn-safe's internal API, the credential a
// background run acts with for the person who started it once that person's sign-in has lapsed.
package executiongrant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/drivenadapters/accountname"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const requestTimeout = 10 * time.Second

// ErrUnavailable is bkn-safe without an address to reach it at.
var ErrUnavailable = errors.New("bkn-safe is not configured")

type executionGrantAccess struct {
	url    string
	client *http.Client
}

// NewExecutionGrantAccess calls bkn-safe at BKN_SAFE_URL.
func NewExecutionGrantAccess() interfaces.ExecutionGrantAccess {
	return NewExecutionGrantAccessAt(os.Getenv(accountname.BknSafeURLEnv))
}

// NewExecutionGrantAccessAt calls bkn-safe at baseURL.
func NewExecutionGrantAccessAt(baseURL string) interfaces.ExecutionGrantAccess {
	return &executionGrantAccess{url: strings.TrimRight(strings.TrimSpace(baseURL), "/"), client: &http.Client{Timeout: requestTimeout}}
}

func (a *executionGrantAccess) Issue(ctx context.Context, req interfaces.ExecutionGrantRequest) (*interfaces.ExecutionGrant, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "IssueExecutionGrant")
	defer span.End()
	body := map[string]any{"id": req.ID, "project_id": req.ProjectID, "run_id": req.RunID, "delegator_id": req.DelegatorID,
		"kn_id": req.KnID, "agent_id": req.AgentID, "ttl_seconds": int64(req.TTL / time.Second)}
	var out struct {
		Credential string    `json:"credential"`
		ExpiresAt  time.Time `json:"expires_at"`
	}
	if err := a.send(ctx, http.MethodPost, "/api/safe/in/v1/execution-grants", body, http.StatusCreated, &out); err != nil {
		span.SetStatus(codes.Error, "Issue execution grant failed")
		return nil, err
	}
	if !strings.HasPrefix(out.Credential, "bex_") {
		span.SetStatus(codes.Error, "Issue execution grant failed")
		return nil, errors.New("bkn-safe issued no execution credential")
	}
	span.SetStatus(codes.Ok, "")
	return &interfaces.ExecutionGrant{Credential: out.Credential, ExpiresAt: out.ExpiresAt}, nil
}

func (a *executionGrantAccess) Revoke(ctx context.Context, id string, projectID string, runID string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "RevokeExecutionGrant")
	defer span.End()
	err := a.send(ctx, http.MethodDelete, "/api/safe/in/v1/execution-grants/"+url.PathEscape(id),
		map[string]string{"project_id": projectID, "run_id": runID}, http.StatusNoContent, nil)
	if err != nil {
		span.SetStatus(codes.Error, "Revoke execution grant failed")
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// send makes one request and decodes a reply of the wanted status into out. The reply body is
// never put in an error: it may carry the credential.
func (a *executionGrantAccess) send(ctx context.Context, method, path string, body any, want int, out any) error {
	if a.url == "" {
		return ErrUnavailable
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, a.url+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != want {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return fmt.Errorf("bkn-safe %s %s: status %d", method, path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(out)
}
