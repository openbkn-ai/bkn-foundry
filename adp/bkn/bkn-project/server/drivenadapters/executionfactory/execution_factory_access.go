// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package executionfactory calls the execution-factory operator-integration
// public API with the caller's token: toolbox impex export and import, and the
// released content, registration and publishing of Skills.
package executionfactory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const (
	// skillMD is the entry file of every Skill package.
	skillMD = "SKILL.md"
	// skillListPageSize is the largest page the Skill list serves.
	skillListPageSize    = "100"
	skillStatusPublished = "published"
)

var (
	efaOnce   sync.Once
	efaAccess interfaces.ExecutionFactoryAccess
)

type executionFactoryAccess struct {
	httpClient rest.HTTPClient
	baseUrl    string
}

// skillContent is the body of GET /skills/{skill_id}/content.
type skillContent struct {
	URL   string `json:"url"`
	Files []struct {
		RelPath string `json:"rel_path"`
	} `json:"files"`
}

// skillFile is the body of POST /skills/{skill_id}/files/read.
type skillFile struct {
	URL string `json:"url"`
}

// skillList is the body of GET /skills.
type skillList struct {
	Data []struct {
		SkillID string `json:"skill_id"`
		Name    string `json:"name"`
		Status  string `json:"status"`
	} `json:"data"`
}

func NewExecutionFactoryAccess(appSetting *common.AppSetting) interfaces.ExecutionFactoryAccess {
	efaOnce.Do(func() {
		efaAccess = &executionFactoryAccess{
			httpClient: common.NewHTTPClient(),
			baseUrl:    appSetting.ExecutionFactoryUrl,
		}
	})
	return efaAccess
}

// ExportToolbox calls GET /impex/export/toolbox/{box_id}.
func (efa *executionFactoryAccess) ExportToolbox(ctx context.Context, token string, boxID string) ([]byte, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ExportToolbox")
	defer span.End()
	span.SetAttributes(attr.Key("box_id").String(boxID))

	httpUrl := fmt.Sprintf("%s/impex/export/toolbox/%s", efa.baseUrl, url.PathEscape(boxID))
	respBody, err := efa.do(ctx, span, http.MethodGet, httpUrl, token, nil, "")
	logger.Debugf("ExportToolbox finished, box_id is [%s]", boxID)
	if err != nil {
		return nil, fmt.Errorf("export toolbox %s: %w", boxID, err)
	}
	return respBody, nil
}

// ImportToolbox calls POST /impex/import/toolbox with the form field mode and
// the export as the multipart file "data".
func (efa *executionFactoryAccess) ImportToolbox(ctx context.Context, token string, data []byte, mode string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ImportToolbox")
	defer span.End()

	body, contentType, err := multipartBody(map[string]string{"mode": mode}, "data", "toolbox.json", data)
	if err != nil {
		return err
	}
	httpUrl := fmt.Sprintf("%s/impex/import/toolbox", efa.baseUrl)
	_, err = efa.do(ctx, span, http.MethodPost, httpUrl, token, body, contentType)
	logger.Debugf("ImportToolbox finished, mode is [%s]", mode)
	if err != nil {
		return fmt.Errorf("import toolbox: %w", err)
	}
	return nil
}

// GetReleasedSkill reads the published release: GET /skills/{skill_id}/content
// lists its files and links SKILL.md, POST /skills/{skill_id}/files/read links
// each other file. The links are object storage download URLs and carry no token.
func (efa *executionFactoryAccess) GetReleasedSkill(ctx context.Context, token string, skillID string) (map[string][]byte, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetReleasedSkill")
	defer span.End()
	span.SetAttributes(attr.Key("skill_id").String(skillID))

	skillUrl := fmt.Sprintf("%s/skills/%s", efa.baseUrl, url.PathEscape(skillID))
	respBody, err := efa.do(ctx, span, http.MethodGet, skillUrl+"/content", token, nil, "")
	if err != nil {
		return nil, fmt.Errorf("read released skill %s: %w", skillID, err)
	}
	content := &skillContent{}
	if err := json.Unmarshal(respBody, content); err != nil {
		return nil, fmt.Errorf("unmarshal content of skill %s: %w", skillID, err)
	}

	files := map[string][]byte{}
	if files[skillMD], err = efa.download(ctx, content.URL); err != nil {
		return nil, fmt.Errorf("download %s of skill %s: %w", skillMD, skillID, err)
	}
	for _, f := range content.Files {
		if f.RelPath == skillMD {
			continue
		}
		reqBody, err := json.Marshal(map[string]string{"rel_path": f.RelPath})
		if err != nil {
			return nil, err
		}
		respBody, err := efa.do(ctx, span, http.MethodPost, skillUrl+"/files/read", token, reqBody, rest.ContentTypeJson)
		if err != nil {
			return nil, fmt.Errorf("read %s of skill %s: %w", f.RelPath, skillID, err)
		}
		link := &skillFile{}
		if err := json.Unmarshal(respBody, link); err != nil {
			return nil, fmt.Errorf("unmarshal %s of skill %s: %w", f.RelPath, skillID, err)
		}
		if files[f.RelPath], err = efa.download(ctx, link.URL); err != nil {
			return nil, fmt.Errorf("download %s of skill %s: %w", f.RelPath, skillID, err)
		}
	}
	logger.Debugf("GetReleasedSkill finished, skill_id is [%s], %d files", skillID, len(files))
	return files, nil
}

// FindPublishedSkill calls GET /skills?name=&status=published. The name filter
// matches substrings, so the exact name is picked from the page.
func (efa *executionFactoryAccess) FindPublishedSkill(ctx context.Context, token string, name string) (string, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "FindPublishedSkill")
	defer span.End()

	query := url.Values{"name": {name}, "status": {skillStatusPublished}, "page_size": {skillListPageSize}}
	httpUrl := fmt.Sprintf("%s/skills?%s", efa.baseUrl, query.Encode())
	respBody, err := efa.do(ctx, span, http.MethodGet, httpUrl, token, nil, "")
	if err != nil {
		return "", fmt.Errorf("find skill %s: %w", name, err)
	}
	list := &skillList{}
	if err := json.Unmarshal(respBody, list); err != nil {
		return "", fmt.Errorf("unmarshal skill list: %w", err)
	}
	for _, s := range list.Data {
		if s.Name == name && s.Status == skillStatusPublished {
			return s.SkillID, nil
		}
	}
	return "", nil
}

// RegisterSkill calls POST /skills with file_type=zip and the package as the multipart file "file".
func (efa *executionFactoryAccess) RegisterSkill(ctx context.Context, token string, archive []byte) (string, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "RegisterSkill")
	defer span.End()

	body, contentType, err := multipartBody(map[string]string{"file_type": "zip"}, "file", "skill.zip", archive)
	if err != nil {
		return "", err
	}
	respBody, err := efa.do(ctx, span, http.MethodPost, efa.baseUrl+"/skills", token, body, contentType)
	if err != nil {
		return "", fmt.Errorf("register skill: %w", err)
	}
	registered := &struct {
		SkillID string `json:"skill_id"`
	}{}
	if err := json.Unmarshal(respBody, registered); err != nil || registered.SkillID == "" {
		return "", fmt.Errorf("register skill: no skill_id in the reply: %s", respBody)
	}
	logger.Debugf("RegisterSkill finished, skill_id is [%s]", registered.SkillID)
	return registered.SkillID, nil
}

// PublishSkill calls PUT /skills/{skill_id}/status with status published.
func (efa *executionFactoryAccess) PublishSkill(ctx context.Context, token string, skillID string) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "PublishSkill")
	defer span.End()
	span.SetAttributes(attr.Key("skill_id").String(skillID))

	reqBody, err := json.Marshal(map[string]string{"status": skillStatusPublished})
	if err != nil {
		return err
	}
	httpUrl := fmt.Sprintf("%s/skills/%s/status", efa.baseUrl, url.PathEscape(skillID))
	if _, err := efa.do(ctx, span, http.MethodPut, httpUrl, token, reqBody, rest.ContentTypeJson); err != nil {
		return fmt.Errorf("publish skill %s: %w", skillID, err)
	}
	return nil
}

// do sends one request to execution-factory and returns the body of a 2xx reply.
func (efa *executionFactoryAccess) do(ctx context.Context, span trace.Span, method string, httpUrl string, token string, body []byte, contentType string) ([]byte, error) {
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: method})
	headers := map[string]string{"Authorization": token}
	if contentType != "" {
		headers[rest.ContentTypeKey] = contentType
	}
	var respCode int
	var respBody []byte
	var err error
	switch method {
	case http.MethodGet:
		respCode, respBody, err = efa.httpClient.GetNoUnmarshal(ctx, httpUrl, nil, headers)
	case http.MethodPost:
		respCode, respBody, err = efa.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, body)
	case http.MethodPut:
		respCode, respBody, err = efa.httpClient.PutNoUnmarshal(ctx, httpUrl, headers, body)
	default:
		return nil, fmt.Errorf("unsupported method %s", method)
	}
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http request failed")
		return nil, err
	}
	if respCode < http.StatusOK || respCode >= http.StatusMultipleChoices {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 2xx")
		efaErr := &interfaces.ExecutionFactoryError{HTTPCode: respCode}
		// A body that is not an execution-factory error still yields the status code.
		_ = json.Unmarshal(respBody, &efaErr.BaseError)
		return nil, efaErr
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return respBody, nil
}

// download fetches an object storage link; it is signed and takes no token.
func (efa *executionFactoryAccess) download(ctx context.Context, link string) ([]byte, error) {
	if link == "" {
		return nil, fmt.Errorf("no download link")
	}
	respCode, respBody, err := efa.httpClient.GetNoUnmarshal(ctx, link, nil, nil)
	if err != nil {
		return nil, err
	}
	if respCode != http.StatusOK {
		return nil, &interfaces.ExecutionFactoryError{HTTPCode: respCode}
	}
	return respBody, nil
}

// multipartBody writes fields and one file part into a multipart body.
func multipartBody(fields map[string]string, fileField string, fileName string, data []byte) ([]byte, string, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			return nil, "", err
		}
	}
	part, err := mw.CreateFormFile(fileField, fileName)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(data); err != nil {
		return nil, "", err
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), mw.FormDataContentType(), nil
}
