// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

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

	"github.com/BurntSushi/toml"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestResourcesAreComplete(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve locale test path")
	}
	if err := i18n.ValidateLocaleDir(filepath.Dir(filename), "zh-CN", "zh-CN", "en-US"); err != nil {
		t.Fatalf("validate locale resources: %v", err)
	}
}

func TestEnglishCatalogCoversUserFacingErrorLiterals(t *testing.T) {
	moduleRoot := filepath.Dir(filepath.Dir(sourceFile(t)))
	messages := loadEnglishMessages(t, filepath.Join(moduleRoot, "locale", "agent-observability.en-US.toml"))
	directories := []string{
		"src/driveradapter/api/httphandler",
		"src/domain/service/evidencesvc",
		"src/domain/service/ledgersvc",
		"src/domain/service/sessionsvc",
		"src/domain/valueobject/evidencevo",
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

func TestTranslateUsesNegotiatedLanguage(t *testing.T) {
	Register()

	english := Translate(WithLanguage(context.Background(), sharedrest.AmericanEnglish), "trace not found")
	chinese := Translate(WithLanguage(context.Background(), sharedrest.SimplifiedChinese), "trace not found")

	if english != "trace not found" || chinese != "Trace 不存在" {
		t.Fatalf("unexpected translations: en=%q zh=%q", english, chinese)
	}
}

func TestTranslateWithDataUsesNegotiatedLanguage(t *testing.T) {
	data := map[string]any{
		"EventType": "agent.interaction.started",
		"RoleField": "question_artifact_ref",
	}
	english := TranslateWithData(
		WithLanguage(context.Background(), sharedrest.AmericanEnglish),
		"artifact_type does not match event link role",
		data,
	)
	chinese := TranslateWithData(
		WithLanguage(context.Background(), sharedrest.SimplifiedChinese),
		"artifact_type does not match event link role",
		data,
	)

	if english != "artifact_type does not match event link role agent.interaction.started.question_artifact_ref" ||
		chinese != "artifact_type 与事件关联角色 agent.interaction.started.question_artifact_ref 不匹配" {
		t.Fatalf("unexpected templated translations: en=%q zh=%q", english, chinese)
	}
}

func TestMissingResourcesUseNonFatalFallback(t *testing.T) {
	Register()
	i18n.RegisterI18n(t.TempDir(), sharedrest.SimplifiedChinese)
	t.Cleanup(register)

	message := Translate(WithLanguage(context.Background(), sharedrest.AmericanEnglish), "trace not found")
	if strings.TrimSpace(message) == "" {
		t.Fatal("missing resources returned an empty fallback")
	}
}

func TestLanguageMiddlewareNegotiatesOneEffectiveLocale(t *testing.T) {
	handler := LanguageMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, sharedrest.GetLanguageByCtx(r.Context()))
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(sharedrest.AcceptLanguageHeader, "en-GB,en;q=0.9,zh-CN;q=0.8")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Body.String() != sharedrest.AmericanEnglish {
		t.Fatalf("effective locale = %q, want %q", response.Body.String(), sharedrest.AmericanEnglish)
	}
}

func TestWrappedHTTPClientOverwritesRawLanguageRange(t *testing.T) {
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Get(sharedrest.AcceptLanguageHeader)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := WrapHTTPClient(server.Client())
	ctx := WithLanguage(context.Background(), sharedrest.AmericanEnglish)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set(sharedrest.AcceptLanguageHeader, "zh-CN,en;q=0.8")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	_ = response.Body.Close()

	if received != sharedrest.AmericanEnglish {
		t.Fatalf("downstream Accept-Language = %q, want %q", received, sharedrest.AmericanEnglish)
	}
}

func sourceFile(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve locale test path")
	}
	return filename
}

func loadEnglishMessages(t *testing.T, path string) map[string]string {
	t.Helper()
	var catalog struct {
		AgentObservability struct {
			Message map[string]string
		}
	}
	if _, err := toml.DecodeFile(path, &catalog); err != nil {
		t.Fatalf("decode English locale: %v", err)
	}
	return catalog.AgentObservability.Message
}

func userFacingErrorLiterals(t *testing.T, directory string) []string {
	t.Helper()
	var messages []string
	scanErrorsNew := strings.Contains(filepath.ToSlash(directory), "/httphandler")
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
