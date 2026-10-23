// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httperrors

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/bytedance/sonic"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

func TestMetadataMergesAndOwnsTopLevelMap(t *testing.T) {
	err := NewHTTPError(t.Context(), 400, PublicError_BadRequest)
	err = err.WithMetadata(nil).WithMetadata(map[string]any{})
	if err.Metadata != nil {
		t.Fatal("empty input allocated metadata")
	}
	nested := map[string]any{"value": "shared"}
	first := map[string]any{"field": "old", "limit": 10, "nested": nested}
	if err.WithMetadata(first) != err {
		t.Fatal("merge broke chaining")
	}
	first["field"] = "caller edit"
	delete(first, "limit")
	previous := err.Metadata
	err = err.WithMetadata(map[string]any{"field": "new"})
	if previous["field"] != "new" {
		t.Fatal("merge replaced the existing metadata map")
	}
	if err.Metadata["field"] != "new" || err.Metadata["limit"] != 10 {
		t.Fatalf("metadata = %#v", err.Metadata)
	}
	if !reflect.DeepEqual(err.Metadata["nested"], nested) {
		t.Fatal("nested value changed")
	}
	if err.Description != "" {
		t.Fatal("metadata triggered rendering")
	}
	err = err.WithMetadata(map[string]any{"field": nil})
	err = err.WithMetadata(nil).WithMetadata(map[string]any{})
	if field, ok := err.Metadata["field"]; !ok || field != nil {
		t.Fatal("nil overwrite was lost")
	}
}

func TestDelayedRenderingAcrossSerializationPaths(t *testing.T) {
	code := "Test.Delayed"
	loadErrorCatalog(t, code, "参数 {{ .field }} 超出 {{ .limit }}", "Field {{ .field }} exceeds {{ .limit }}")
	registerTestCode(t, code)
	for _, tc := range []struct {
		lang        i18n.Language
		description string
	}{
		{i18n.SimplifiedChinese, "参数 title 超出 10"},
		{i18n.AmericanEnglish, "Field title exceeds 10"},
	} {
		t.Run(string(tc.lang), func(t *testing.T) {
			err := NewHTTPError(i18n.WithLanguage(t.Context(), tc.lang), http.StatusBadRequest, code)
			err = err.WithMetadata(map[string]any{"field": "title"})
			renderErr := err.Render()
			incomplete := err.BaseError
			if renderErr != nil || !strings.Contains(incomplete.Description, "<no value>") {
				t.Fatalf("default missing key: %#v, %v", incomplete, renderErr)
			}
			err = err.WithMetadata(map[string]any{"limit": 10}).WithErrorDetails("public detail")
			for _, value := range []any{err, *err} {
				for _, marshal := range []func(any) ([]byte, error){json.Marshal, sonic.Marshal} {
					data, marshalErr := marshal(value)
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					body := assertErrorBody(t, data, code, tc.description)
					metadata, ok := body["metadata"].(map[string]any)
					if !ok || metadata["field"] != "title" || metadata["limit"] != float64(10) || body["error_details"] != "public detail" {
						t.Fatalf("response lost parameters/details: %s", data)
					}
					var dto BaseError
					if decodeErr := json.Unmarshal(data, &dto); decodeErr != nil {
						t.Fatal(decodeErr)
					}
					roundTrip, marshalErr := marshal(dto)
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					assertErrorBody(t, roundTrip, code, tc.description)
				}
			}
			assertErrorBody(t, []byte(err.Error()), code, tc.description)
			assertErrorBody(t, []byte(err.BaseError.Error()), code, incomplete.Description)
			nested, marshalErr := sonic.Marshal(map[string]any{"error": err})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			var envelope map[string]json.RawMessage
			if decodeErr := json.Unmarshal(nested, &envelope); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			assertErrorBody(t, envelope["error"], code, tc.description)
			renderErr = err.Render()
			if renderErr != nil {
				t.Fatal(renderErr)
			}
			if err.Metadata["field"] != "title" || err.Description != tc.description {
				t.Fatalf("rendered error: %#v", err.BaseError)
			}
		})
	}
}

func TestRegistrationRejectsMissingAndDuplicateCodesAtomically(t *testing.T) {
	code := "Test.Register"
	loadErrorCatalog(t, code, "参数 {{ .field }}", "Field {{ .field }}")
	assertRegistrationPanic(t, func() { Register([]string{code, code}) })
	assertRegistrationPanic(t, func() { _ = NewHTTPError(t.Context(), 400, code) })
	assertRegistrationPanic(t, func() { Register([]string{code, "Test.Missing"}) })
	assertRegistrationPanic(t, func() { _ = NewHTTPError(t.Context(), 400, code) })
	registerTestCode(t, code)
	assertRegistrationPanic(t, func() { Register([]string{code}) })
}

func loadErrorCatalog(t *testing.T, code, zhDescription, enDescription string) {
	t.Helper()
	i18n.RegisterI18n(&i18n.I18nMessages{Locale: i18n.SimplifiedChinese, Messages: map[string]string{code: zhDescription}})
	i18n.RegisterI18n(&i18n.I18nMessages{Locale: i18n.AmericanEnglish, Messages: map[string]string{code: enDescription}})
}

func registerTestCode(t *testing.T, code string) {
	t.Helper()
	Register([]string{code})
	t.Cleanup(func() { delete(registeredErrors, code) })
}

func assertRegistrationPanic(t *testing.T, run func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Error("expected registration panic")
		}
	}()
	run()
}

func assertErrorBody(t *testing.T, data []byte, code, description string) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode response %q: %v", data, err)
	}
	if body["error_code"] != code || body["description"] != description {
		t.Fatalf("response = %s", data)
	}
	for _, internal := range []string{"solution", "error_link", "HTTPCode", "Language", "BaseError", "DescriptionTemplateData", "SolutionTemplateData", "language"} {
		if _, exists := body[internal]; exists {
			t.Fatalf("response exposes %s: %s", internal, data)
		}
	}
	return body
}

func TestBaseErrorIsPlainDTOAndIgnoresLegacyFields(t *testing.T) {
	var dto BaseError
	if err := json.Unmarshal([]byte(`{"error_code":"Unregistered.Code","description":"already rendered","error_details":"{{ .field }}","solution":"legacy","error_link":"legacy"}`), &dto); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatal(err)
	}
	if body["description"] != "already rendered" || body["error_details"] != "{{ .field }}" {
		t.Fatalf("DTO was rendered: %s", encoded)
	}
	for _, removed := range []string{"solution", "error_link", "metadata"} {
		if _, exists := body[removed]; exists {
			t.Fatalf("unexpected field %s", removed)
		}
	}
}

func TestRegisterValidationDoesNotPublishPartialBatch(t *testing.T) {
	code := "Test.Batch"
	loadErrorCatalog(t, code, "参数", "Parameter")
	if _, err := validateRegistration([]string{code, "Test.Missing"}); err == nil {
		t.Fatal("missing message accepted")
	}
	if _, exists := registeredErrors[code]; exists {
		t.Fatal("failed validation published a partial batch")
	}
	if _, err := validateRegistration([]string{code, code}); err == nil {
		t.Fatal("duplicate code accepted")
	}
	if _, exists := registeredErrors[code]; exists {
		t.Fatal("duplicate batch published state")
	}
}

func TestSuccessfulRegistrationValidationDoesNotPublishState(t *testing.T) {
	code := "Test.Candidate"
	loadErrorCatalog(t, code, "参数", "Parameter")
	pending, err := validateRegistration([]string{code})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := pending[code]; !exists {
		t.Fatal("candidate missing code")
	}
	if _, exists := registeredErrors[code]; exists {
		t.Fatal("validation installed candidate")
	}
}
