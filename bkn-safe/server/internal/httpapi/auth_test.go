// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
)

func hydraAcceptLoginError(t *testing.T, body string) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	_, err := auth.NewHydraAdmin(srv.URL).AcceptLogin(context.Background(), "challenge", "subject", false)
	if err == nil {
		t.Fatal("AcceptLogin error = nil, want Hydra error")
	}
	return err
}

func TestIsExpiredLoginRequestRecognizesHydraExpiredChallenge(t *testing.T) {
	err := hydraAcceptLoginError(t, `{"error":"request_unauthorized","reason":"The login request has expired, please try again.","status_code":401}`)
	if !isExpiredLoginRequest(err) {
		t.Fatal("expired Hydra login request must be rendered as a restart-login prompt")
	}
}

func TestIsExpiredLoginRequestRejectsOtherHydraUnauthorizedResponses(t *testing.T) {
	err := hydraAcceptLoginError(t, `{"error":"invalid_client","error_description":"admin authentication is required","status_code":401}`)
	if isExpiredLoginRequest(err) {
		t.Fatal("non-expired Hydra 401 must remain an internal authentication-service error")
	}
}
