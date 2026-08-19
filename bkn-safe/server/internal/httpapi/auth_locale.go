// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

const (
	authLocaleCookieName = "openbkn_locale"
	authLocaleCookieTTL  = 365 * 24 * time.Hour
)

// applyAuthLocale resolves the presentation language for an authentication
// request without changing any authentication decision. Explicit page input
// wins over OIDC ui_locales, then the first-party preference cookie, then the
// language already selected from Accept-Language by the shared middleware.
func applyAuthLocale(c *gin.Context, requestURL string) string {
	language := setAuthLocaleContext(c, requestURL)

	http.SetCookie(c.Writer, &http.Cookie{
		Name:     authLocaleCookieName,
		Value:    language,
		Path:     "/",
		MaxAge:   int(authLocaleCookieTTL.Seconds()),
		Expires:  time.Now().Add(authLocaleCookieTTL),
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(c.Request),
	})
	return language
}

func setAuthLocaleContext(c *gin.Context, requestURL string) string {
	language := resolveAuthLocale(c, requestURL)
	c.Request = c.Request.WithContext(sharedrest.WithLanguage(c.Request.Context(), language))
	return language
}

func resolveAuthLocale(c *gin.Context, requestURL string) string {
	explicit := c.Query("lang")
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		explicit = c.PostForm("lang")
	}
	if language, ok := normalizeAuthLocale(explicit); ok {
		return language
	}
	if language, ok := authLocaleFromRequestURL(requestURL); ok {
		return language
	}
	if cookie, err := c.Request.Cookie(authLocaleCookieName); err == nil {
		if language, ok := normalizeAuthLocale(cookie.Value); ok {
			return language
		}
	}
	return sharedrest.GetLanguageByCtx(c.Request.Context())
}

func normalizeAuthLocale(value string) (string, bool) {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "-"))
	switch {
	case normalized == "zh", normalized == "zh-cn", normalized == "zh-hans",
		strings.HasPrefix(normalized, "zh-cn-"), strings.HasPrefix(normalized, "zh-hans-"):
		return sharedrest.SimplifiedChinese, true
	case normalized == "en", strings.HasPrefix(normalized, "en-"):
		return sharedrest.AmericanEnglish, true
	default:
		return "", false
	}
}

func authLocaleFromRequestURL(requestURL string) (string, bool) {
	parsed, err := url.Parse(requestURL)
	if err != nil {
		return "", false
	}
	for _, candidate := range strings.Fields(parsed.Query().Get("ui_locales")) {
		if language, ok := normalizeAuthLocale(candidate); ok {
			return language, true
		}
	}
	return "", false
}

func requestIsHTTPS(request *http.Request) bool {
	return request.TLS != nil || strings.EqualFold(request.Header.Get("X-Forwarded-Proto"), "https")
}

func setAuthLocaleLinks(data *authPageData, path string, values url.Values) {
	data.ChineseURL = authLocaleURL(path, sharedrest.SimplifiedChinese, values)
	data.EnglishURL = authLocaleURL(path, sharedrest.AmericanEnglish, values)
}

func localizedAuthPageDataFor(c *gin.Context, path string, values url.Values) authPageData {
	data := localizedAuthPageData(c)
	setAuthLocaleLinks(&data, path, values)
	return data
}

func loginAuthPageData(c *gin.Context, challenge string) authPageData {
	data := localizedAuthPageDataFor(c, "/login", url.Values{"login_challenge": {challenge}})
	data.Challenge = challenge
	return data
}

func changePasswordAuthPageData(c *gin.Context, challenge, account string) authPageData {
	values := url.Values{"login_challenge": {challenge}}
	if account != "" {
		values.Set("account", account)
	}
	data := localizedAuthPageDataFor(c, "/change-password", values)
	data.Challenge = challenge
	data.Account = account
	return data
}

func consentAuthPageData(c *gin.Context, challenge string) authPageData {
	data := localizedAuthPageDataFor(c, "/consent", url.Values{"consent_challenge": {challenge}})
	data.Challenge = challenge
	return data
}

func deviceAuthPageData(c *gin.Context, challenge, userCode string) authPageData {
	values := url.Values{"device_challenge": {challenge}}
	if userCode != "" {
		values.Set("user_code", userCode)
	}
	data := localizedAuthPageDataFor(c, "/device", values)
	data.Challenge = challenge
	data.UserCode = userCode
	return data
}

func authLocaleURL(path, language string, values url.Values) string {
	query := make(url.Values, len(values)+1)
	for key, items := range values {
		query[key] = append([]string(nil), items...)
	}
	query.Set("lang", language)
	return path + "?" + query.Encode()
}
