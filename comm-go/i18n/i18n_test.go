// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package i18n

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidateLocaleDirUsesConfiguredDefaultLocale(t *testing.T) {
	dir := writeLocaleFiles(t, map[string]string{
		"errors.zh-CN.toml": "[Error]\nDescription = '请求失败'\n",
		"errors.en-US.toml": "[Error]\nDescription = 'Request failed'\nExtra = 'unexpected'\n",
	})

	err := ValidateLocaleDir(dir, "zh-CN")
	if err == nil || !strings.Contains(err.Error(), "unexpected messageId Error.Extra") {
		t.Fatalf("expected deterministic baseline validation error, got %v", err)
	}
}

func TestValidateLocaleDirChecksTemplateFields(t *testing.T) {
	dir := writeLocaleFiles(t, map[string]string{
		"errors.zh-CN.toml": "[Error]\nDescription = '参数 {{ .name }}'\n",
		"errors.en-US.toml": "[Error]\nDescription = 'Parameter {{ .id }}'\n",
	})

	err := ValidateLocaleDir(dir, "zh-CN")
	if err == nil || !strings.Contains(err.Error(), "template field") {
		t.Fatalf("expected template field validation error, got %v", err)
	}
}

func TestValidateLocaleDirRequiresDeclaredLocales(t *testing.T) {
	dir := writeLocaleFiles(t, map[string]string{
		"errors.zh-CN.toml": "[Error]\nDescription = '请求失败'\n",
	})

	err := ValidateLocaleDir(dir, "zh-CN", "zh-CN", "en-US")
	if err == nil || !strings.Contains(err.Error(), "required locale en-US is not present") {
		t.Fatalf("expected required-locale validation error, got %v", err)
	}
}

func TestTranslateFallsBackWithoutFatal(t *testing.T) {
	dir := writeLocaleFiles(t, map[string]string{
		"errors.zh-CN.toml": "[Error]\nDescription = '请求失败：{{ .name }}'\nSolution = '请重试'\n",
		"errors.en-US.toml": "[Error]\nDescription = 'Request failed: {{ .name }}'\nSolution = 'Try again'\n",
	})

	RegisterI18n(dir, "zh-CN")
	if got := Translate("en-US", "Error.Description", map[string]interface{}{"name": "model"}); got != "Request failed: model" {
		t.Fatalf("unexpected translation: %q", got)
	}
	if got := Translate("fr-FR", "Error.Description", map[string]interface{}{"name": "model"}); got != "请求失败：model" {
		t.Fatalf("expected default-locale fallback, got %q", got)
	}
	if got := Translate("en-US", "Unknown.Description", nil); got != genericEnglishMessage {
		t.Fatalf("expected generic English fallback, got %q", got)
	}
	if got := Translate("zh-CN", "Error.Description", nil); got != genericChineseMessage {
		t.Fatalf("expected template execution fallback, got %q", got)
	}
}

func TestSetDefaultLocaleChangesRuntimeFallback(t *testing.T) {
	dir := writeLocaleFiles(t, map[string]string{
		"errors.zh-CN.toml": "[Error]\nDescription = '请求失败'\n",
		"errors.en-US.toml": "[Error]\nDescription = 'Request failed'\n",
	})

	RegisterI18n(dir, "zh-CN")
	SetDefaultLocale("en-US")
	t.Cleanup(func() { SetDefaultLocale("zh-CN") })

	if got := Translate("fr-FR", "Error.Description", nil); got != "Request failed" {
		t.Fatalf("expected configured-default fallback, got %q", got)
	}
}

func TestServiceLocaleDirectories(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	localeDirs := []string{
		"adp/bkn/bkn-backend/server/locale",
		"adp/bkn/ontology-query/server/locale",
		"adp/vega/vega-backend/server/locale",
	}
	for _, localeDir := range localeDirs {
		t.Run(localeDir, func(t *testing.T) {
			if err := ValidateLocaleDir(filepath.Join(repoRoot, localeDir), "zh-CN", "zh-CN", "en-US"); err != nil {
				t.Fatalf("validate locale directory: %v", err)
			}
		})
	}
}

func writeLocaleFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}
