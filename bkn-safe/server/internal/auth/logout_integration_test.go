// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"testing"
)

// Run only against a disposable local Hydra. No deployed credentials are used.
// BKN_SAFE_TEST_HYDRA_PUBLIC_URL / BKN_SAFE_TEST_HYDRA_ADMIN_URL opt in.
func TestStudioLogoutRealHydra(t *testing.T) {
	public, admin := os.Getenv("BKN_SAFE_TEST_HYDRA_PUBLIC_URL"), os.Getenv("BKN_SAFE_TEST_HYDRA_ADMIN_URL")
	if public == "" || admin == "" {
		t.Skip("set disposable local Hydra URLs to run OAuth integration")
	}
	for _, raw := range []string{public, admin} {
		u, err := url.Parse(raw)
		if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
			t.Fatal("integration test requires a disposable loopback Hydra")
		}
	}
	request := func(method, target string, body any) map[string]any {
		t.Helper()
		var payload io.Reader
		if body != nil {
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			payload = bytes.NewReader(data)
		}
		req, err := http.NewRequestWithContext(t.Context(), method, target, payload)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			t.Fatalf("Hydra %s %s: %d", method, req.URL.Path, res.StatusCode)
		}
		var out map[string]any
		if res.StatusCode != 204 {
			if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	for _, id := range []string{"openbkn-studio", "logout-regression-other"} {
		request("POST", admin+"/admin/clients", map[string]any{
			"client_id": id, "token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code", "refresh_token"},
			"response_types": []string{"code"}, "scope": "openid offline", "redirect_uris": []string{"http://127.0.0.1:14500/callback"},
			"post_logout_redirect_uris": []string{"http://127.0.0.1:14500/done"},
		})
		defer request("DELETE", admin+"/admin/clients/"+id, nil)
	}
	mint := func(clientID string, jar http.CookieJar) map[string]string {
		t.Helper()
		if jar == nil {
			jar, _ = cookiejar.New(nil)
		}
		client := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
		verifier := "local-2913-pkce-verifier-with-at-least-43-characters"
		hash := sha256.Sum256([]byte(verifier))
		params := url.Values{"client_id": {clientID}, "response_type": {"code"}, "scope": {"openid offline"},
			"redirect_uri": {"http://127.0.0.1:14500/callback"}, "state": {"logout-regression-state"},
			"code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}}
		target := public + "/oauth2/auth?" + params.Encode()
		var code string
		for i := 0; i < 10; i++ {
			parsed, _ := url.Parse(target)
			if v := parsed.Query().Get("code"); v != "" {
				code = v
				break
			}
			if challenge := parsed.Query().Get("login_challenge"); challenge != "" {
				result := request("PUT", admin+"/admin/oauth2/auth/requests/login/accept?login_challenge="+url.QueryEscape(challenge), map[string]any{"subject": "logout-test-user", "remember": true, "remember_for": 3600})
				target = result["redirect_to"].(string)
				continue
			}
			if challenge := parsed.Query().Get("consent_challenge"); challenge != "" {
				result := request("PUT", admin+"/admin/oauth2/auth/requests/consent/accept?consent_challenge="+url.QueryEscape(challenge), map[string]any{"grant_scope": []string{"openid", "offline"}, "remember": false})
				target = result["redirect_to"].(string)
				continue
			}
			req, _ := http.NewRequestWithContext(t.Context(), "GET", target, nil)
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			location, err := res.Location()
			if err != nil {
				t.Fatalf("authorization did not redirect: %d", res.StatusCode)
			}
			target = location.String()
		}
		if code == "" {
			t.Fatal("authorization did not produce a code")
		}
		res, err := http.PostForm(public+"/oauth2/token", url.Values{"client_id": {clientID}, "grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {"http://127.0.0.1:14500/callback"}, "code_verifier": {verifier}})
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("token exchange status %d", res.StatusCode)
		}
		var tokens struct {
			Access  string `json:"access_token"`
			Refresh string `json:"refresh_token"`
			IDToken string `json:"id_token"`
		}
		if err := json.NewDecoder(res.Body).Decode(&tokens); err != nil {
			t.Fatal(err)
		}
		if tokens.Access == "" || tokens.Refresh == "" {
			t.Fatal("missing test tokens")
		}
		return map[string]string{"access": tokens.Access, "refresh": tokens.Refresh, "id": tokens.IDToken}
	}
	browserJar, _ := cookiejar.New(nil)
	current, device, other := mint("openbkn-studio", browserJar), mint("openbkn-studio", nil), mint("logout-regression-other", browserJar)
	revoker := NewStudioLogout(NewHydraAdmin(admin), public)
	if subject, err := revoker.Logout(t.Context(), current["access"], current["refresh"], current["id"]); err != nil || subject != "logout-test-user" {
		t.Fatalf("logout: subject=%q err=%v", subject, err)
	}
	if _, err := revoker.admin.VerifyToken(t.Context(), current["access"]); err == nil {
		t.Fatal("old access token survived logout")
	}
	res, err := http.PostForm(public+"/oauth2/token", url.Values{"client_id": {"openbkn-studio"}, "grant_type": {"refresh_token"}, "refresh_token": {current["refresh"]}})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var failure struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(res.Body).Decode(&failure)
	if res.StatusCode != 400 || failure.Error != "invalid_grant" {
		t.Fatalf("old refresh token accepted: %d %s", res.StatusCode, failure.Error)
	}
	for name, tokens := range map[string]map[string]string{"other device": device, "other client": other} {
		if _, err := revoker.admin.VerifyToken(t.Context(), tokens["access"]); err != nil {
			t.Fatalf("%s access invalidated: %v", name, err)
		}
		info, err := revoker.inspect(t.Context(), tokens["refresh"], "refresh_token")
		if err != nil || !info.GetActive() {
			t.Fatalf("%s refresh invalidated: %v", name, err)
		}
	}
	if subject, err := revoker.Logout(t.Context(), current["access"], current["refresh"], current["id"]); err != nil || subject != "" {
		t.Fatalf("repeat logout: %q %v", subject, err)
	}
	t.Run("rotation before logout still revokes the current grant", func(t *testing.T) {
		res, err := http.PostForm(public+"/oauth2/token", url.Values{
			"client_id": {"openbkn-studio"}, "grant_type": {"refresh_token"}, "refresh_token": {device["refresh"]},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var rotated struct {
			Access  string `json:"access_token"`
			Refresh string `json:"refresh_token"`
			IDToken string `json:"id_token"`
		}
		if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&rotated) != nil || rotated.Refresh == "" {
			t.Fatal("test refresh did not rotate credentials")
		}
		// A browser without Web Locks may still hold the pre-refresh snapshot.
		if _, err := revoker.Logout(t.Context(), device["access"], device["refresh"], device["id"]); err != nil {
			t.Fatal(err)
		}
		for use, token := range map[string]string{"access_token": rotated.Access, "refresh_token": rotated.Refresh} {
			info, err := revoker.inspect(t.Context(), token, use)
			if err != nil || info.GetActive() {
				t.Fatalf("rotated %s survived stale-pair logout: %v", use, err)
			}
		}
	})
}
