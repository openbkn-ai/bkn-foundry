// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type schemaReadAccess struct {
	baseURL string
	client  *http.Client
}

// NewSchemaReadAccess uses the existing deployment-configured BKN backend.
func NewSchemaReadAccess() interfaces.SchemaReadAccess {
	conf := config.NewConfigLoader()
	return newSchemaReadAccess(conf.BknBackend.BuildURL("/api/bkn-backend"))
}

func newSchemaReadAccess(baseURL string) *schemaReadAccess {
	return &schemaReadAccess{
		baseURL: baseURL,
		client: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (s *schemaReadAccess) ReadSchema(ctx context.Context, knID, collection string, query url.Values) (int, []byte, error) {
	if knID == "" || knID == "." || knID == ".." {
		return 0, nil, fmt.Errorf("invalid knowledge network id")
	}
	auth, ok := common.GetAccountAuthContextFromCtx(ctx)
	if !ok || auth.AccountID == "" {
		return 0, nil, fmt.Errorf("authenticated schema subject is missing")
	}
	operation := "bkn.knowledge_network.get"
	switch collection {
	case "":
	case "object-types":
		operation = "bkn.object_type.list"
	case "relation-types":
		operation = "bkn.relation_type.list"
	case "object-metrics":
		operation = "bkn.object_metric.list"
	default:
		return 0, nil, fmt.Errorf("unsupported schema collection")
	}
	target := s.baseURL + "/in/v1/knowledge-networks/" + url.PathEscape(knID)
	if collection != "" {
		target += "/" + collection
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, nil, err
	}
	request.URL.RawQuery = query.Encode()
	// Never forward caller-supplied identity headers or the raw bearer token. The
	// verified subject follows the same path as the existing BKN detail adapters.
	for key, value := range common.GetHeaderForChildOperation(ctx, operation, 1) {
		request.Header.Set(key, value)
	}
	request.Header.Set("user_id", auth.AccountID)
	request.Header.Set("Accept-Language", string(common.GetLanguageFromCtx(ctx)))
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	const maxSchemaBytes = 32 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxSchemaBytes+1))
	if err != nil {
		return 0, nil, err
	}
	if len(body) > maxSchemaBytes {
		return 0, nil, fmt.Errorf("schema response exceeds size limit")
	}
	return response.StatusCode, body, nil
}
