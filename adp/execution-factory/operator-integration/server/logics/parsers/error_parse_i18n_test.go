package parsers

import (
	"context"
	"errors"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

func TestOpenAPIMultiErrorDetailsUseRequestLanguage(t *testing.T) {
	tests := []struct {
		name     string
		language string
		want     string
	}{
		{name: "Chinese", language: i18n.SimplifiedChinese, want: "错误1：first failure"},
		{name: "English", language: i18n.AmericanEnglish, want: "Error 1: first failure"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := i18n.WithLanguage(context.Background(), test.language)
			multiErr := openapi3.MultiError{errors.New("first failure"), errors.New("second failure")}

			httpErr := parseOpenAPILoadError(ctx, &multiErr)
			details, ok := httpErr.ErrorDetails.([]string)
			if !ok || len(details) != 2 {
				t.Fatalf("details = %#v, want two strings", httpErr.ErrorDetails)
			}
			if details[0] != test.want {
				t.Fatalf("details[0] = %q, want %q", details[0], test.want)
			}
		})
	}
}
