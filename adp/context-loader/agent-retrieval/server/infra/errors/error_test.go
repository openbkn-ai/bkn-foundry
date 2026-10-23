// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestSharedHTTPErrorEnvelope(t *testing.T) {
	for _, language := range []i18n.Language{i18n.SimplifiedChinese, i18n.AmericanEnglish} {
		t.Run(string(language), func(t *testing.T) {
			ctx := i18n.WithLanguage(context.Background(), language)
			details := map[string]any{"reason": "{{ literal }}"}
			err := NewHTTPError(ctx, http.StatusBadRequest, ErrExtCommonNameInvalid, details)
			if err.BaseError.Description != "" {
				t.Fatal("constructor rendered the description")
			}
			if err.Lang != language {
				t.Fatalf("language = %q", err.Lang)
			}
			if renderErr := err.Render(); renderErr != nil {
				t.Fatal(renderErr)
			}
			if err.BaseError.Description == "" || strings.Contains(err.BaseError.Description, "<no value>") {
				t.Fatalf("description = %q", err.BaseError.Description)
			}
			data, encodeErr := json.Marshal(err)
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			var body map[string]any
			if decodeErr := json.Unmarshal(data, &body); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if body["error_code"] != err.BaseError.ErrorCode || body["description"] != err.BaseError.Description || body["error_details"].(map[string]any)["reason"] != "{{ literal }}" {
				t.Fatalf("body = %s", data)
			}
			for _, legacy := range []string{"code", "details", "solution", "link", "error_link"} {
				if _, exists := body[legacy]; exists {
					t.Fatalf("legacy field %s present", legacy)
				}
			}
		})
	}
}

func TestPublicStatusMessagesRender(t *testing.T) {
	for status := range errCodeMap {
		for _, language := range []i18n.Language{i18n.SimplifiedChinese, i18n.AmericanEnglish} {
			err := DefaultHTTPError(i18n.WithLanguage(context.Background(), language), status, nil)
			if renderErr := err.Render(); renderErr != nil {
				t.Fatalf("status %d language %s: %v", status, language, renderErr)
			}
			if err.BaseError.Description == "" {
				t.Fatalf("status %d: empty description", status)
			}
		}
	}
}

func TestDependencyResponseBodyIsPrivate(t *testing.T) {
	public := DefaultHTTPError(context.Background(), http.StatusBadGateway, "dependency request failed")
	dependency := &DependencyError{Err: public, Body: []byte("internal-body-sentinel")}
	var extracted *rest.HTTPError
	if !errors.As(fmt.Errorf("read failed: %w", dependency), &extracted) || extracted != public {
		t.Fatal("shared error not unwrapped")
	}
	encoded, err := json.Marshal(dependency)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "internal-body-sentinel") || !strings.Contains(string(encoded), `"error_code":"Public.BadGateway"`) {
		t.Fatalf("body = %s", encoded)
	}
}
