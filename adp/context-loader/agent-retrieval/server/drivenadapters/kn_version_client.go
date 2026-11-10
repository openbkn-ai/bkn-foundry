// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"net/url"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/knversion"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// knVersionClient wraps the HTTP client of bkn-backend and ontology-query. For
// a tool call that carries a knversion.Selector it adds kn_version and
// draft_revision to every read or query request and reports every response
// back to the selector, which pins the version from the first one. Without a
// selector it changes nothing. Only the verbs that read are wrapped.
type knVersionClient struct {
	interfaces.HTTPClient
}

func newKnVersionClient(inner interfaces.HTTPClient) interfaces.HTTPClient {
	return &knVersionClient{HTTPClient: inner}
}

// prepare returns the context that reports responses and the URL carrying the
// version parameters. The proxy-account resolution is not a versioned read.
func (c *knVersionClient) prepare(ctx context.Context, rawURL string, qv url.Values) (context.Context, string) {
	sel := knversion.FromContext(ctx)
	if sel == nil {
		return ctx, rawURL
	}
	ctx = rest.WithResponseObserver(ctx, sel.Observe)
	if strings.Contains(rawURL, "/proxy-account/") {
		return ctx, rawURL
	}
	version, revision := sel.Params()
	if version == "" {
		return ctx, rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ctx, rawURL
	}
	q := u.Query()
	// kn_version already says which branch to read; bkn-backend refuses
	// branch=draft next to it, and branch=main adds nothing.
	dropRedundantBranch(q)
	dropRedundantBranch(qv)
	q.Set(knversion.QueryVersion, version)
	if revision != "" {
		q.Set(knversion.QueryDraftRevision, revision)
	}
	u.RawQuery = q.Encode()
	return ctx, u.String()
}

func (c *knVersionClient) Get(ctx context.Context, rawURL string, qv url.Values, h map[string]string) (int, interface{}, error) {
	ctx, rawURL = c.prepare(ctx, rawURL, qv)
	return c.HTTPClient.Get(ctx, rawURL, qv, h)
}

func (c *knVersionClient) GetNoUnmarshal(ctx context.Context, rawURL string, qv url.Values, h map[string]string) (int, []byte, error) {
	ctx, rawURL = c.prepare(ctx, rawURL, qv)
	return c.HTTPClient.GetNoUnmarshal(ctx, rawURL, qv, h)
}

func (c *knVersionClient) GetBytes(ctx context.Context, rawURL string, qv url.Values, h map[string]string) (int, []byte, error) {
	ctx, rawURL = c.prepare(ctx, rawURL, qv)
	return c.HTTPClient.GetBytes(ctx, rawURL, qv, h)
}

func (c *knVersionClient) Post(ctx context.Context, rawURL string, h map[string]string, body interface{}) (int, interface{}, error) {
	ctx, rawURL = c.prepare(ctx, rawURL, nil)
	return c.HTTPClient.Post(ctx, rawURL, h, body)
}

func (c *knVersionClient) PostNoUnmarshal(ctx context.Context, rawURL string, h map[string]string, body interface{}) (int, []byte, error) {
	ctx, rawURL = c.prepare(ctx, rawURL, nil)
	return c.HTTPClient.PostNoUnmarshal(ctx, rawURL, h, body)
}

func (c *knVersionClient) PostBytes(ctx context.Context, rawURL string, h map[string]string, body interface{}) (int, []byte, error) {
	ctx, rawURL = c.prepare(ctx, rawURL, nil)
	return c.HTTPClient.PostBytes(ctx, rawURL, h, body)
}

func dropRedundantBranch(q url.Values) {
	if q == nil {
		return
	}
	if b := q.Get("branch"); b == "main" || b == knversion.Draft {
		q.Del("branch")
	}
}
