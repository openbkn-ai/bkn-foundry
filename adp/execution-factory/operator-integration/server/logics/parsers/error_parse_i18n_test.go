package parsers

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestOpenAPIMultiErrorDetailsUseRequestLanguage(t *testing.T) {
	tests := []struct {
		name     string
		language string
		want     string
	}{
		{name: "Chinese", language: sharedrest.SimplifiedChinese, want: "错误1：first failure"},
		{name: "English", language: sharedrest.AmericanEnglish, want: "Error 1: first failure"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := sharedrest.WithLanguage(context.Background(), test.language)
			multiErr := openapi3.MultiError{stderrors.New("first failure"), stderrors.New("second failure")}

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
