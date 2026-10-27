// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package knowledge_base reads Enterprise knowledge base identities from kb-api's in-cluster
// API, so a knowledge network can mount a knowledge base by id and an import can find one by
// name (#2576). Only identities are read: id and name, never content.
package knowledge_base

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

const (
	// inClusterPrefix is kb-api's in-cluster API, which ingress does not route.
	inClusterPrefix = "/api/kb/in/v1"
	resourceType    = "knowledge_base"
	// pageSize is kb-api's largest page; namePages bounds a name search so a very common
	// substring cannot turn one import into an unbounded scan.
	pageSize  = 100
	namePages = 20
)

type knowledgeBaseAccess struct {
	httpClient rest.HTTPClient
	baseURL    string
}

// NewKnowledgeBaseAccess creates kb-api access at baseURL (KB_API_URL, e.g.
// http://kb-api.openbkn:8001). An empty baseURL is a deployment without the Enterprise
// knowledge base: every call answers interfaces.ErrKnowledgeBaseUnavailable.
func NewKnowledgeBaseAccess(baseURL string) interfaces.KnowledgeBaseAccess {
	return &knowledgeBaseAccess{
		httpClient: common.NewHTTPClient(),
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
	}
}

type knowledgeBaseEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (e knowledgeBaseEntry) brief() *interfaces.KnowledgeBaseBrief {
	return &interfaces.KnowledgeBaseBrief{ID: e.ID, Name: e.Name}
}

func (kba *knowledgeBaseAccess) GetKnowledgeBase(ctx context.Context, kbID string) (*interfaces.KnowledgeBaseBrief, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetKnowledgeBase")
	defer span.End()

	if kba.baseURL == "" {
		return nil, interfaces.ErrKnowledgeBaseUnavailable
	}
	httpURL := fmt.Sprintf("%s%s/knowledge-bases/%s", kba.baseURL, inClusterPrefix, url.PathEscape(strings.TrimSpace(kbID)))
	respCode, body, err := kba.get(ctx, httpURL, nil)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get knowledge base failed")
		return nil, err
	}
	if respCode == http.StatusNotFound {
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return nil, nil
	}
	if respCode != http.StatusOK {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		return nil, fmt.Errorf("kb-api get knowledge base returned HTTP %d", respCode)
	}
	var entry knowledgeBaseEntry
	if err := json.Unmarshal(body, &entry); err != nil || entry.ID == "" {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal knowledge base failed")
		return nil, fmt.Errorf("kb-api get knowledge base returned an unreadable body")
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return entry.brief(), nil
}

// FindKnowledgeBasesByName pages kb-api's authorization-resource listing, which matches names
// by substring, and keeps the exact matches.
func (kba *knowledgeBaseAccess) FindKnowledgeBasesByName(ctx context.Context, name string) ([]*interfaces.KnowledgeBaseBrief, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "FindKnowledgeBasesByName")
	defer span.End()

	if kba.baseURL == "" {
		return nil, interfaces.ErrKnowledgeBaseUnavailable
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	httpURL := kba.baseURL + inClusterPrefix + "/authorization-resources"
	matches := make([]*interfaces.KnowledgeBaseBrief, 0)
	for page := 0; page < namePages; page++ {
		query := url.Values{
			"resource_type": {resourceType},
			"name":          {name},
			"offset":        {strconv.Itoa(page * pageSize)},
			"limit":         {strconv.Itoa(pageSize)},
		}
		respCode, body, err := kba.get(ctx, httpURL, query)
		if err != nil {
			oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http list knowledge bases failed")
			return nil, err
		}
		if respCode != http.StatusOK {
			oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
			return nil, fmt.Errorf("kb-api list knowledge bases returned HTTP %d", respCode)
		}
		var list struct {
			Entries []knowledgeBaseEntry `json:"entries"`
			Total   int                  `json:"total"`
		}
		if err := json.Unmarshal(body, &list); err != nil {
			oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal knowledge bases failed")
			return nil, fmt.Errorf("kb-api list knowledge bases returned an unreadable body")
		}
		for _, entry := range list.Entries {
			if entry.Name == name && entry.ID != "" {
				matches = append(matches, entry.brief())
			}
		}
		if len(list.Entries) < pageSize || (page+1)*pageSize >= list.Total {
			oteltrace.AddHttpAttrs4Ok(span, respCode)
			return matches, nil
		}
	}
	return nil, fmt.Errorf("kb-api name search for %q exceeded %d pages", name, namePages)
}

func (kba *knowledgeBaseAccess) get(ctx context.Context, httpURL string, query url.Values) (int, []byte, error) {
	headers := map[string]string{interfaces.CONTENT_TYPE_NAME: interfaces.CONTENT_TYPE_JSON}
	respCode, body, err := kba.httpClient.GetNoUnmarshal(ctx, httpURL, query, headers)
	logger.Debugf("kb-api GET %s finished, response code is [%d], %s", httpURL, respCode, common.SafeErrorSummary(err))
	if err != nil {
		common.LogSafeError(ctx, "kb-api request failed", err)
		return respCode, nil, fmt.Errorf("kb-api request failed")
	}
	return respCode, body, nil
}
