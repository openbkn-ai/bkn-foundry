package service

import (
	stderrors "errors"
	"strings"
	"testing"

	apperrors "oss-gateway/pkg/errors"
)

func TestURLValidationErrorsUseStableClientParameters(t *testing.T) {
	tests := []struct {
		name      string
		validate  func() error
		parameter string
	}{
		{
			name:      "storage id",
			validate:  func() error { return validateStorageID("") },
			parameter: "storage_id",
		},
		{
			name:      "object key",
			validate:  func() error { return validateObjectKey("") },
			parameter: "object_key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var validationErr *StorageValidationError
			if err := tt.validate(); !stderrors.As(err, &validationErr) {
				t.Fatalf("expected StorageValidationError, got %T: %v", err, err)
			}
			if validationErr.Code != apperrors.InvalidParam.Code {
				t.Fatalf("expected code %s, got %s", apperrors.InvalidParam.Code, validationErr.Code)
			}
			if got := validationErr.Params["Parameter"]; got != tt.parameter {
				t.Fatalf("expected parameter %q, got %q", tt.parameter, got)
			}
			if validationErr.Err == nil {
				t.Fatal("expected the underlying validation diagnostic to be retained")
			}
			if !stderrors.Is(validationErr, validationErr.Err) {
				t.Fatal("expected the underlying validation diagnostic to be unwrapped")
			}
			if !strings.Contains(validationErr.Error(), validationErr.Err.Error()) {
				t.Fatalf("expected error text to retain diagnostic %q, got %q", validationErr.Err, validationErr)
			}
		})
	}
}
