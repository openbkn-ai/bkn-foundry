// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bknbackend

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const (
	importArchive = "bkn.tar"
	// noLimit turns off paging of a bkn-backend list (interfaces.NO_LIMIT).
	noLimit = "-1"
)

var (
	bbaOnce   sync.Once
	bbaAccess interfaces.BknBackendAccess
)

type bknBackendAccess struct {
	httpClient rest.HTTPClient
	baseUrl    string
	// raw sends the requests whose reply headers are read; nil is a default client.
	raw *http.Client
}

func NewBknBackendAccess(appSetting *common.AppSetting) interfaces.BknBackendAccess {
	bbaOnce.Do(func() {
		bbaAccess = &bknBackendAccess{
			httpClient: common.NewHTTPClient(),
			baseUrl:    appSetting.BknBackendUrl,
		}
	})
	return bbaAccess
}

// exportBknSpec is the BKN specification every lineage is kept in. A package without a declared
// specification is read by the 2.x rules, which have no place for value types and other 3.0
// declarations a project's draft writes; exporting every network as 3.0 keeps one set of rules for
// main, the project branches and what a release reads back.
const exportBknSpec = "3.0"

// GetKN calls GET /knowledge-networks/{kn_id}?branch=main.
func (bba *bknBackendAccess) GetKN(ctx context.Context, token string, knID string) (*interfaces.KnowledgeNetwork, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetKN")
	defer span.End()
	span.SetAttributes(attr.Key("kn_id").String(knID))

	httpUrl := fmt.Sprintf("%s/%s", bba.networksUrl(), url.PathEscape(knID))
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: http.MethodGet})

	query := url.Values{"branch": []string{interfaces.GIT_MAIN_BRANCH}}
	respCode, respBody, err := bba.httpClient.GetNoUnmarshal(ctx, httpUrl, query, map[string]string{"Authorization": token})
	logger.Debugf("GetKN finished, kn_id is [%s], response code is [%d]", knID, respCode)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get network failed")
		return nil, fmt.Errorf("get network %s: %w", knID, err)
	}
	if respCode != http.StatusOK {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		return nil, backendError(respCode, respBody)
	}

	network := &interfaces.KnowledgeNetwork{}
	if err := json.Unmarshal(respBody, network); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal network failed")
		return nil, fmt.Errorf("unmarshal network %s: %w", knID, err)
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return network, nil
}

// ListKNIDs calls GET /knowledge-networks?limit=-1, which lists only the networks the caller can see.
func (bba *bknBackendAccess) ListKNIDs(ctx context.Context, token string) ([]string, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ListKNIDs")
	defer span.End()

	httpUrl := bba.networksUrl()
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: http.MethodGet})

	query := url.Values{"limit": []string{noLimit}}
	respCode, respBody, err := bba.httpClient.GetNoUnmarshal(ctx, httpUrl, query, map[string]string{"Authorization": token})
	logger.Debugf("ListKNIDs finished, response code is [%d]", respCode)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http list networks failed")
		return nil, fmt.Errorf("list networks: %w", err)
	}
	if respCode != http.StatusOK {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		return nil, backendError(respCode, respBody)
	}

	list := &struct {
		Entries []*interfaces.KnowledgeNetwork `json:"entries"`
	}{}
	if err := json.Unmarshal(respBody, list); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal networks failed")
		return nil, fmt.Errorf("unmarshal networks: %w", err)
	}
	ids := make([]string, 0, len(list.Entries))
	for _, network := range list.Entries {
		ids = append(ids, network.ID)
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return ids, nil
}

func (bba *bknBackendAccess) networksUrl() string {
	return fmt.Sprintf("%s/knowledge-networks", bba.baseUrl)
}

func (bba *bknBackendAccess) schedulesUrl(knID string) string {
	return fmt.Sprintf("%s/knowledge-networks/%s/action-schedules", bba.baseUrl, url.PathEscape(knID))
}

func backendError(respCode int, respBody []byte) error {
	backendErr := &interfaces.BknBackendError{HTTPCode: respCode}
	// A body that is not a bkn-backend error still yields the status code.
	_ = json.Unmarshal(respBody, &backendErr.BaseError)
	return backendErr
}

// multipartTar packs files into a tar, in path order, inside a multipart body.
func multipartTar(files map[string][]byte) ([]byte, string, error) {
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	now := time.Now()
	for _, p := range paths {
		header := &tar.Header{Name: p, Mode: 0644, Size: int64(len(files[p])), ModTime: now, Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(header); err != nil {
			return nil, "", err
		}
		if _, err := tw.Write(files[p]); err != nil {
			return nil, "", err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, "", err
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreatePart(map[string][]string{
		"Content-Disposition": {fmt.Sprintf(`form-data; name="file"; filename="%s"`, importArchive)},
		"Content-Type":        {"application/octet-stream"},
	})
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(archive.Bytes()); err != nil {
		return nil, "", err
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), mw.FormDataContentType(), nil
}
