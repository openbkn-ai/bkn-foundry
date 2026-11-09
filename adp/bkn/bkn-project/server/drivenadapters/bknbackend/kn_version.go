// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bknbackend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// replaceImportMode removes from the draft what the package does not have.
const replaceImportMode = "replace"

// NewKNVersionAccess calls the version API of bkn-backend (design bkn-docs@122618a, section 13).
func NewKNVersionAccess(appSetting *common.AppSetting) interfaces.KNVersionAccess {
	return &bknBackendAccess{httpClient: common.NewHTTPClient(), baseUrl: appSetting.BknBackendUrl}
}

func (bba *bknBackendAccess) networkUrl(knID string, rest ...string) string {
	u := fmt.Sprintf("%s/knowledge-networks/%s", bba.baseUrl, url.PathEscape(knID))
	for _, r := range rest {
		u += "/" + r
	}
	return u
}

// call sends one request and decodes a reply of the wanted status into out.
func (bba *bknBackendAccess) call(ctx context.Context, name string, method string, httpUrl string, query url.Values,
	token string, body any, want int, out any) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, name)
	defer span.End()
	if len(query) > 0 {
		httpUrl += "?" + query.Encode()
	}
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: method})
	headers := map[string]string{"Authorization": token}
	var respCode int
	var respBody []byte
	var err error
	if method == http.MethodGet {
		respCode, respBody, err = bba.httpClient.GetNoUnmarshal(ctx, httpUrl, nil, headers)
	} else {
		headers[rest.ContentTypeKey] = rest.ContentTypeJson
		if body == nil {
			body = map[string]any{}
		}
		respCode, respBody, err = bba.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, body)
	}
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http request failed")
		return fmt.Errorf("%s: %w", name, err)
	}
	if respCode != want {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unexpected status")
		return backendError(respCode, respBody)
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal reply failed")
			return fmt.Errorf("%s: unmarshal reply: %w", name, err)
		}
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return nil
}

// CreateVersionedKN calls POST /knowledge-networks with {"id", "name", "branch": "main", "versioning": true}.
func (bba *bknBackendAccess) CreateVersionedKN(ctx context.Context, token string, id string, name string) (string, error) {
	created := &struct {
		ID string `json:"id"`
	}{}
	body := map[string]any{"id": id, "name": name, "branch": interfaces.GIT_MAIN_BRANCH, "versioning": true}
	if err := bba.call(ctx, "CreateVersionedKN", http.MethodPost, bba.networksUrl(), nil, token, body, http.StatusCreated, created); err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", fmt.Errorf("create network %s: no id in the reply", name)
	}
	return created.ID, nil
}

// EnableVersioning calls POST /knowledge-networks/{kn_id}/versioning.
func (bba *bknBackendAccess) EnableVersioning(ctx context.Context, token string, knID string, notes string) error {
	return bba.call(ctx, "EnableVersioning", http.MethodPost, bba.networkUrl(knID, "versioning"), nil, token,
		map[string]any{"notes": notes}, http.StatusOK, nil)
}

// GetVersioning calls GET /knowledge-networks/{kn_id}/versioning.
func (bba *bknBackendAccess) GetVersioning(ctx context.Context, token string, knID string) (*interfaces.KNVersioning, error) {
	out := &interfaces.KNVersioning{}
	if err := bba.call(ctx, "GetVersioning", http.MethodGet, bba.networkUrl(knID, "versioning"), nil, token, nil, http.StatusOK, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListVersions calls GET /knowledge-networks/{kn_id}/versions page by page.
func (bba *bknBackendAccess) ListVersions(ctx context.Context, token string, knID string) ([]*interfaces.KNVersion, error) {
	const page = 100
	all := []*interfaces.KNVersion{}
	for offset := 0; ; offset += page {
		out := &struct {
			Entries []*interfaces.KNVersion `json:"entries"`
		}{}
		query := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(page)}}
		if err := bba.call(ctx, "ListVersions", http.MethodGet, bba.networkUrl(knID, "versions"), query, token, nil, http.StatusOK, out); err != nil {
			return nil, err
		}
		all = append(all, out.Entries...)
		if len(out.Entries) < page {
			return all, nil
		}
	}
}

// GetVersion calls GET /knowledge-networks/{kn_id}/versions/{version}.
func (bba *bknBackendAccess) GetVersion(ctx context.Context, token string, knID string, version int) (*interfaces.KNVersion, error) {
	out := &interfaces.KNVersion{}
	if err := bba.call(ctx, "GetVersion", http.MethodGet, bba.networkUrl(knID, "versions", strconv.Itoa(version)), nil, token, nil, http.StatusOK, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ReplaceDraft calls POST /bkns?import_mode=replace&expected_draft_revision=N[&overwrite_changes=true]
// with the files as a tar; the network id in network.bkn names the target.
func (bba *bknBackendAccess) ReplaceDraft(ctx context.Context, token string, files map[string][]byte,
	req interfaces.KNReplaceImport) (*interfaces.BKNImportResult, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ReplaceDraft")
	defer span.End()

	query := url.Values{"import_mode": {replaceImportMode}, "expected_draft_revision": {strconv.FormatInt(req.ExpectedDraftRevision, 10)}}
	if req.Overwrite {
		query.Set("overwrite_changes", "true")
	}
	httpUrl := fmt.Sprintf("%s/bkns?%s", bba.baseUrl, query.Encode())
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: http.MethodPost})
	body, contentType, err := multipartTar(files)
	if err != nil {
		return nil, err
	}
	respCode, respBody, err := bba.httpClient.PostNoUnmarshal(ctx, httpUrl,
		map[string]string{"Authorization": token, rest.ContentTypeKey: contentType}, body)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http import BKN failed")
		return nil, fmt.Errorf("replace draft: %w", err)
	}
	if respCode != http.StatusOK {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		return nil, backendError(respCode, respBody)
	}
	result := &interfaces.BKNImportResult{}
	if err := json.Unmarshal(respBody, result); err != nil {
		return nil, fmt.Errorf("unmarshal import response: %w", err)
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return result, nil
}

// exportVersionQuery names the version to export. Until bkn-backend exports by kn_version, the draft
// and the running version are read through the branches they are kept in: draft and main.
func exportVersionQuery(version string) url.Values {
	switch version {
	case interfaces.KN_VERSION_DRAFT:
		return url.Values{"branch": {interfaces.KN_VERSION_DRAFT}}
	case interfaces.KN_VERSION_RUNNING:
		return url.Values{"branch": {interfaces.GIT_MAIN_BRANCH}}
	}
	return url.Values{"kn_version": {version}}
}

// ExportVersion calls GET /bkns/{kn_id}?<version>&bkn_spec=3.0 (see exportVersionQuery).
func (bba *bknBackendAccess) ExportVersion(ctx context.Context, token string, knID string, version string) (map[string][]byte, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ExportVersion")
	defer span.End()
	span.SetAttributes(attr.Key("kn_id").String(knID), attr.Key("kn_version").String(version))

	httpUrl := fmt.Sprintf("%s/bkns/%s", bba.baseUrl, url.PathEscape(knID))
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: http.MethodGet})
	query := exportVersionQuery(version)
	query.Set("bkn_spec", exportBknSpec)
	respCode, respBody, err := bba.httpClient.GetNoUnmarshal(ctx, httpUrl, query, map[string]string{"Authorization": token})
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http export BKN failed")
		return nil, fmt.Errorf("export BKN %s at %s: %w", knID, version, err)
	}
	if respCode != http.StatusOK {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		return nil, backendError(respCode, respBody)
	}
	files, err := common.ReadBKNTar(respBody)
	if err != nil {
		return nil, fmt.Errorf("read export tar of %s: %w", knID, err)
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return files, nil
}

// Publish calls POST /knowledge-networks/{kn_id}/versions.
func (bba *bknBackendAccess) Publish(ctx context.Context, token string, knID string,
	req interfaces.KNVersionPublish) (*interfaces.KNVersionPublished, error) {
	out := &interfaces.KNVersionPublished{}
	if err := bba.call(ctx, "PublishKNVersion", http.MethodPost, bba.networkUrl(knID, "versions"), nil, token, req, http.StatusCreated, out); err != nil {
		return nil, err
	}
	return out, nil
}

// Activate calls POST /knowledge-networks/{kn_id}/versions/{version}/activate.
func (bba *bknBackendAccess) Activate(ctx context.Context, token string, knID string, version int, expectedRunning int,
	notes string) (*interfaces.KNVersionActivated, error) {
	out := &interfaces.KNVersionActivated{}
	body := map[string]any{"expected_running_version": expectedRunning, "notes": notes}
	if err := bba.call(ctx, "ActivateKNVersion", http.MethodPost, bba.networkUrl(knID, "versions", strconv.Itoa(version), "activate"),
		nil, token, body, http.StatusOK, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ResetDraft calls POST /knowledge-networks/{kn_id}/draft/reset.
func (bba *bknBackendAccess) ResetDraft(ctx context.Context, token string, knID string, fromVersion int,
	expectedDraftRevision int64, overwrite bool) error {
	body := map[string]any{"from_version": fromVersion, "expected_draft_revision": expectedDraftRevision, "overwrite_changes": overwrite}
	return bba.call(ctx, "ResetKNDraft", http.MethodPost, bba.networkUrl(knID, "draft", "reset"), nil, token, body, http.StatusOK, nil)
}

// ReferencedResources calls GET /knowledge-networks/{kn_id}/versions/referenced-resources.
func (bba *bknBackendAccess) ReferencedResources(ctx context.Context, token string, knID string) ([]string, error) {
	out := &struct {
		Entries []struct {
			ID string `json:"id"`
		} `json:"entries"`
	}{}
	if err := bba.call(ctx, "ReferencedResources", http.MethodGet, bba.networkUrl(knID, "versions", "referenced-resources"),
		nil, token, nil, http.StatusOK, out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Entries))
	for _, e := range out.Entries {
		ids = append(ids, e.ID)
	}
	return ids, nil
}
