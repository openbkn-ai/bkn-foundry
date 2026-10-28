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
	importMode    = "overwrite"
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

// ExportBKN calls GET /bkns/{kn_id}?branch=main&bkn_spec=3.0, which replies with a tar of the
// network files. bkn-backend refuses a network a 3.0 package cannot hold as it is
// (ExportNeedsNetworkCheck), naming what to fix.
func (bba *bknBackendAccess) ExportBKN(ctx context.Context, token string, knID string) (map[string][]byte, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ExportBKN")
	defer span.End()
	span.SetAttributes(attr.Key("kn_id").String(knID))

	httpUrl := fmt.Sprintf("%s/bkns/%s", bba.baseUrl, url.PathEscape(knID))
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: http.MethodGet})

	query := url.Values{"branch": []string{interfaces.GIT_MAIN_BRANCH}, "bkn_spec": []string{exportBknSpec}}
	respCode, respBody, err := bba.httpClient.GetNoUnmarshal(ctx, httpUrl, query, map[string]string{"Authorization": token})
	logger.Debugf("ExportBKN finished, kn_id is [%s], response code is [%d]", knID, respCode)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http export BKN failed")
		return nil, fmt.Errorf("export BKN %s: %w", knID, err)
	}
	if respCode != http.StatusOK {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		return nil, backendError(respCode, respBody)
	}

	files, err := common.ReadBKNTar(respBody)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Read export tar failed")
		return nil, fmt.Errorf("read export tar of %s: %w", knID, err)
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return files, nil
}

// ImportBKN calls POST /bkns?branch=main&import_mode=overwrite with the files as a tar in the
// multipart field "file".
func (bba *bknBackendAccess) ImportBKN(ctx context.Context, token string, files map[string][]byte) (*interfaces.BKNImportResult, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ImportBKN")
	defer span.End()

	query := url.Values{"branch": []string{interfaces.GIT_MAIN_BRANCH}, "import_mode": []string{importMode}}
	httpUrl := fmt.Sprintf("%s/bkns?%s", bba.baseUrl, query.Encode())
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: http.MethodPost})

	body, contentType, err := multipartTar(files)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, 0, "InternalError", "Build import archive failed")
		return nil, err
	}
	headers := map[string]string{"Authorization": token, rest.ContentTypeKey: contentType}
	respCode, respBody, err := bba.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, body)
	logger.Debugf("ImportBKN finished, response code is [%d]", respCode)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http import BKN failed")
		return nil, fmt.Errorf("import BKN: %w", err)
	}
	if respCode != http.StatusOK {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		return nil, backendError(respCode, respBody)
	}

	result := &interfaces.BKNImportResult{}
	if err := json.Unmarshal(respBody, result); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal import response failed")
		return nil, fmt.Errorf("unmarshal import response: %w", err)
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return result, nil
}

// ListSchedules calls GET /knowledge-networks/{kn_id}/action-schedules?branch=main&limit=-1.
func (bba *bknBackendAccess) ListSchedules(ctx context.Context, token string, knID string) ([]*interfaces.ActionSchedule, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ListSchedules")
	defer span.End()
	span.SetAttributes(attr.Key("kn_id").String(knID))

	httpUrl := bba.schedulesUrl(knID)
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: http.MethodGet})

	query := url.Values{"branch": []string{interfaces.GIT_MAIN_BRANCH}, "limit": []string{noLimit}}
	respCode, respBody, err := bba.httpClient.GetNoUnmarshal(ctx, httpUrl, query, map[string]string{"Authorization": token})
	logger.Debugf("ListSchedules finished, kn_id is [%s], response code is [%d]", knID, respCode)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http list schedules failed")
		return nil, fmt.Errorf("list schedules of %s: %w", knID, err)
	}
	if respCode != http.StatusOK {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		return nil, backendError(respCode, respBody)
	}

	list := &struct {
		Entries []*interfaces.ActionSchedule `json:"entries"`
	}{}
	if err := json.Unmarshal(respBody, list); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal schedules failed")
		return nil, fmt.Errorf("unmarshal schedules of %s: %w", knID, err)
	}
	if list.Entries == nil {
		list.Entries = []*interfaces.ActionSchedule{}
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return list.Entries, nil
}

// CreateSchedule calls POST /knowledge-networks/{kn_id}/action-schedules?branch=main.
func (bba *bknBackendAccess) CreateSchedule(ctx context.Context, token string, knID string,
	schedule *interfaces.ActionSchedule) (string, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "CreateSchedule")
	defer span.End()
	span.SetAttributes(attr.Key("kn_id").String(knID))

	query := url.Values{"branch": []string{interfaces.GIT_MAIN_BRANCH}}
	httpUrl := fmt.Sprintf("%s?%s", bba.schedulesUrl(knID), query.Encode())
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: http.MethodPost})

	body := *schedule
	body.ID = ""
	headers := map[string]string{"Authorization": token, rest.ContentTypeKey: rest.ContentTypeJson}
	respCode, respBody, err := bba.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, body)
	logger.Debugf("CreateSchedule finished, kn_id is [%s], response code is [%d]", knID, respCode)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http create schedule failed")
		return "", fmt.Errorf("create schedule %s on %s: %w", schedule.Name, knID, err)
	}
	if respCode != http.StatusCreated {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 201")
		return "", backendError(respCode, respBody)
	}

	created := &struct {
		ID string `json:"id"`
	}{}
	if err := json.Unmarshal(respBody, created); err != nil || created.ID == "" {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "No id in the reply")
		return "", fmt.Errorf("create schedule %s on %s: no id in the reply: %s", schedule.Name, knID, respBody)
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return created.ID, nil
}

// CreateKN calls POST /knowledge-networks with {"id", "name", "branch": "main"}.
func (bba *bknBackendAccess) CreateKN(ctx context.Context, token string, id string, name string) (string, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "CreateKN")
	defer span.End()
	span.SetAttributes(attr.Key("kn_id").String(id))

	httpUrl := bba.networksUrl()
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: http.MethodPost})

	headers := map[string]string{"Authorization": token, rest.ContentTypeKey: rest.ContentTypeJson}
	// bkn-backend validates the branch before it would default it.
	body := map[string]string{"id": id, "name": name, "branch": interfaces.GIT_MAIN_BRANCH}
	respCode, respBody, err := bba.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, body)
	logger.Debugf("CreateKN finished, kn_id is [%s], response code is [%d]", id, respCode)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http create network failed")
		return "", fmt.Errorf("create network %s: %w", name, err)
	}
	if respCode != http.StatusCreated {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 201")
		return "", backendError(respCode, respBody)
	}

	created := &struct {
		ID string `json:"id"`
	}{}
	if err := json.Unmarshal(respBody, created); err != nil || created.ID == "" {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "No id in the reply")
		return "", fmt.Errorf("create network %s: no id in the reply: %s", name, respBody)
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return created.ID, nil
}

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

// SetScheduleStatus calls PUT /knowledge-networks/{kn_id}/action-schedules/{schedule_id}/status.
func (bba *bknBackendAccess) SetScheduleStatus(ctx context.Context, token string, knID string,
	scheduleID string, status string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "SetScheduleStatus")
	defer span.End()
	span.SetAttributes(attr.Key("kn_id").String(knID), attr.Key("schedule_id").String(scheduleID))

	httpUrl := fmt.Sprintf("%s/%s/status", bba.schedulesUrl(knID), url.PathEscape(scheduleID))
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: http.MethodPut})

	headers := map[string]string{"Authorization": token, rest.ContentTypeKey: rest.ContentTypeJson}
	respCode, respBody, err := bba.httpClient.PutNoUnmarshal(ctx, httpUrl, headers, map[string]string{"status": status})
	logger.Debugf("SetScheduleStatus finished, schedule_id is [%s], response code is [%d]", scheduleID, respCode)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http set schedule status failed")
		return fmt.Errorf("set schedule %s of %s %s: %w", scheduleID, knID, status, err)
	}
	if respCode != http.StatusOK {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		return backendError(respCode, respBody)
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return nil
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
