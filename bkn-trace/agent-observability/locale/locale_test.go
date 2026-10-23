// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package locale

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

func TestMessagesAreComplete(t *testing.T) {
	Register()
	Register()
	if err := i18n.ValidateMessageBundles(); err != nil {
		t.Fatalf("validate locale resources: %v", err)
	}
}

func TestEnglishMessageBundleCoversUserFacingErrorLiterals(t *testing.T) {
	moduleRoot := filepath.Dir(filepath.Dir(sourceFile(t)))
	messages := loadEnglishMessages()
	directories := []string{
		"src/driveradapter/api/httphandler",
		"src/domain/service/evidencesvc",
		"src/domain/service/ledgersvc",
		"src/domain/service/sessionsvc",
		"src/domain/valueobject/evidencevo",
		"src/port/driven/ievidenceledger",
	}

	missing := make(map[string]struct{})
	for _, directory := range directories {
		for _, message := range userFacingErrorLiterals(t, filepath.Join(moduleRoot, directory)) {
			if _, ok := messages[message]; !ok {
				missing[message] = struct{}{}
			}
		}
	}
	if len(missing) == 0 {
		return
	}
	values := make([]string, 0, len(missing))
	for message := range missing {
		values = append(values, message)
	}
	sort.Strings(values)
	t.Fatalf("English locale is missing user-facing messages:\n- %s", strings.Join(values, "\n- "))
}

func TestSharedRenderUsesNegotiatedLanguage(t *testing.T) {
	Register()
	localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(WithLanguage(context.Background(), i18n.AmericanEnglish)), "AgentObservability.Message.trace not found", nil)
	if renderErr != nil {
		logger.Errorf("localized message rendering failed: %v", renderErr)
		localizedText, _ = i18n.Render(i18n.ResolveLanguage(string(i18n.GetLanguageByCtx(WithLanguage(context.Background(), i18n.AmericanEnglish)))), "AgentObservability.Message.request failed", nil)
	}
	english := localizedText
	localizedText, renderErr = i18n.Render(i18n.GetLanguageByCtx(WithLanguage(context.Background(), i18n.SimplifiedChinese)), "AgentObservability.Message.trace not found", nil)
	if renderErr != nil {
		logger.Errorf("localized message rendering failed: %v", renderErr)
		localizedText, _ = i18n.Render(i18n.ResolveLanguage(string(i18n.GetLanguageByCtx(WithLanguage(context.Background(), i18n.SimplifiedChinese)))), "AgentObservability.Message.request failed", nil)
	}
	chinese := localizedText

	if english != "trace not found" || chinese != "Trace 不存在" {
		t.Fatalf("unexpected translations: en=%q zh=%q", english, chinese)
	}
}

func TestSharedRenderWithDataUsesNegotiatedLanguage(t *testing.T) {
	data := map[string]any{
		"EventType": "agent.interaction.started",
		"RoleField": "question_artifact_ref",
	}
	localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(
		WithLanguage(context.Background(), i18n.AmericanEnglish)), "AgentObservability.Message.artifact_type does not match event link role", data,
	)
	if renderErr != nil {
		logger.Errorf("localized message rendering failed: %v", renderErr)
		localizedText, _ = i18n.Render(i18n.ResolveLanguage(string(i18n.GetLanguageByCtx(WithLanguage(context.Background(), i18n.AmericanEnglish)))), "AgentObservability.Message.request failed", nil)
	}
	english := localizedText
	localizedText, renderErr = i18n.Render(i18n.GetLanguageByCtx(
		WithLanguage(context.Background(), i18n.SimplifiedChinese)), "AgentObservability.Message.artifact_type does not match event link role", data,
	)
	if renderErr != nil {
		logger.Errorf("localized message rendering failed: %v", renderErr)
		localizedText, _ = i18n.Render(i18n.ResolveLanguage(string(i18n.GetLanguageByCtx(WithLanguage(context.Background(), i18n.SimplifiedChinese)))), "AgentObservability.Message.request failed", nil)
	}
	chinese := localizedText

	if english != "artifact_type does not match event link role agent.interaction.started.question_artifact_ref" ||
		chinese != "artifact_type 与事件关联角色 agent.interaction.started.question_artifact_ref 不匹配" {
		t.Fatalf("unexpected templated translations: en=%q zh=%q", english, chinese)
	}
}

func TestMissingMessagesFailStartupWithoutReplacingMessageBundle(t *testing.T) {
	Register()
	localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(WithLanguage(context.Background(), i18n.AmericanEnglish)), "AgentObservability.Message.trace not found", nil)
	if renderErr != nil {
		logger.Errorf("localized message rendering failed: %v", renderErr)
		localizedText, _ = i18n.Render(i18n.ResolveLanguage(string(i18n.GetLanguageByCtx(WithLanguage(context.Background(), i18n.AmericanEnglish)))), "AgentObservability.Message.request failed", nil)
	}
	before := localizedText
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected missing resources to fail startup")
			}
		}()
		i18n.RegisterI18n(&i18n.I18nMessages{Locale: i18n.SimplifiedChinese, Messages: map[string]string{"Invalid": "{{"}})
	}()
	localizedText, renderErr = i18n.Render(i18n.GetLanguageByCtx(WithLanguage(context.Background(), i18n.AmericanEnglish)), "AgentObservability.Message.trace not found", nil)
	if renderErr != nil {
		logger.Errorf("localized message rendering failed: %v", renderErr)
		localizedText, _ = i18n.Render(i18n.ResolveLanguage(string(i18n.GetLanguageByCtx(WithLanguage(context.Background(), i18n.AmericanEnglish)))), "AgentObservability.Message.request failed", nil)
	}
	after := localizedText
	if before == "" || after != before {
		t.Fatalf("message bundle changed after rejected loading: %q -> %q", before, after)
	}
}

func TestLanguageMiddlewareNegotiatesOneEffectiveLocale(t *testing.T) {
	handler := LanguageMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, string(i18n.GetLanguageByCtx(r.Context())))
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(i18n.AcceptLanguageHeader, "en-GB,en;q=0.9,zh-CN;q=0.8")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Body.String() != string(i18n.AmericanEnglish) {
		t.Fatalf("effective locale = %q, want %q", response.Body.String(), i18n.AmericanEnglish)
	}
}

func TestWrappedHTTPClientOverwritesRawLanguageRange(t *testing.T) {
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Get(i18n.AcceptLanguageHeader)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := WrapHTTPClient(server.Client())
	ctx := WithLanguage(context.Background(), i18n.AmericanEnglish)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set(i18n.AcceptLanguageHeader, "zh-CN,en;q=0.8")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	_ = response.Body.Close()

	if received != string(i18n.AmericanEnglish) {
		t.Fatalf("downstream Accept-Language = %q, want %q", received, i18n.AmericanEnglish)
	}
}

func sourceFile(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	return filename
}

func loadEnglishMessages() map[string]string {
	messages := make(map[string]string)
	for key, value := range agentObservabilityEnUS.Messages {
		if id, ok := strings.CutPrefix(key, messagePrefix); ok {
			messages[id] = value
		}
	}
	return messages
}

func userFacingErrorLiterals(t *testing.T, directory string) []string {
	t.Helper()
	var messages []string
	scanErrorsNew := strings.Contains(filepath.ToSlash(directory), "/httphandler") ||
		strings.Contains(filepath.ToSlash(directory), "/ievidenceledger")
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.KeyValueExpr:
				if identifier, ok := value.Key.(*ast.Ident); ok && identifier.Name == "Message" {
					if message, ok := stringLiteral(value.Value); ok {
						messages = append(messages, message)
					}
				}
			case *ast.CallExpr:
				name := calledFunctionName(value.Fun)
				index := -1
				switch name {
				case "New":
					if scanErrorsNew {
						index = 0
					}
				case "domainError":
					index = 1
				case "NewValidationError", "NewTemplatedValidationError":
					index = 2
				case "writeLifecycleError", "writeObservabilityError", "writeQueryAuthorizationError":
					index = len(value.Args) - 1
				}
				if index >= 0 && index < len(value.Args) {
					if message, ok := stringLiteral(value.Args[index]); ok {
						messages = append(messages, message)
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(fmt.Errorf("scan user-facing errors: %w", err))
	}
	return messages
}

func stringLiteral(expression ast.Expr) (string, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

func calledFunctionName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return value.Sel.Name
	default:
		return ""
	}
}

func TestUnknownMessagesReturnLocalizedFailure(t *testing.T) {
	for _, test := range []struct {
		language i18n.Language
		want     string
	}{
		{i18n.AmericanEnglish, "Request failed."},
		{i18n.SimplifiedChinese, "请求失败。"},
	} {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(WithLanguage(context.Background(), test.language)), "AgentObservability.Message.unregistered-internal-diagnostic", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
			localizedText, _ = i18n.Render(i18n.ResolveLanguage(string(i18n.GetLanguageByCtx(WithLanguage(context.Background(), test.language)))), "AgentObservability.Message.request failed", nil)
		}
		got := localizedText
		if got != test.want {
			t.Fatalf("%s: got %q, want %q", test.language, got, test.want)
		}
	}
}

func TestLedgerScopeErrorIsLocalized(t *testing.T) {
	for _, test := range []struct {
		language i18n.Language
		want     string
	}{
		{i18n.AmericanEnglish, "evidence interaction or operation does not exist"},
		{i18n.SimplifiedChinese, "证据所属交互或操作不存在"},
	} {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(WithLanguage(context.Background(), test.language)), "AgentObservability.Message.evidence interaction or operation does not exist", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
			localizedText, _ = i18n.Render(i18n.ResolveLanguage(string(i18n.GetLanguageByCtx(WithLanguage(context.Background(), test.language)))), "AgentObservability.Message.request failed", nil)
		}
		got := localizedText
		if got != test.want {
			t.Fatalf("%s: got %q, want %q", test.language, got, test.want)
		}
	}
}
