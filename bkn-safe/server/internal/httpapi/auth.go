// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	_ "embed"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/accesslog"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/locale"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

// registerAuth mounts the hydra login/consent/device provider pages. hydra is
// configured with URLS_LOGIN/CONSENT/DEVICE_VERIFICATION pointing here. The
// pages are server-rendered (no SPA), styled to match the BKN Studio console:
// device shows the user_code to confirm, consent shows the requesting client +
// requested scopes with explicit Authorize/Decline.
func registerAuth(r *gin.Engine, p *auth.Provider, h *auth.HydraAdmin, accessStore *accesslog.Store) {
	r.GET(openBKNLogoPath, serveOpenBKNLogo)
	r.GET(loginBackgroundPath, serveLoginBackground)
	r.GET("/login", func(c *gin.Context) { showLogin(c, h) })
	r.POST("/login", func(c *gin.Context) { doLogin(c, p, accessStore) })
	r.GET("/change-password", showChangePassword)
	r.POST("/change-password", func(c *gin.Context) { doChangePassword(c, p, accessStore) })
	r.GET("/consent", func(c *gin.Context) { showConsent(c, p) })
	r.POST("/consent", func(c *gin.Context) { doConsent(c, p) })
	r.GET("/device", showDevice)
	r.POST("/device", func(c *gin.Context) { doDevice(c, h) })
	r.GET("/device/success", showDeviceSuccess)
}

// pageCSS is the shared light shell (centered panel), following the BKN Studio
// console design language: #2563eb primary, light product backdrop, white
// 20px-radius panel, AntD-like 8px fields/buttons, and the OpenBKN logo.
const pageCSS = `<style>
:root{color-scheme:light}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
  position:relative;isolation:isolate;background:#f7f9fc;
  color:#152239;font:15px/1.5 "Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;
  -webkit-font-smoothing:antialiased}
body:before{content:"";position:fixed;inset:0;z-index:-2;background-image:
  linear-gradient(90deg,rgba(248,250,252,.96) 0%,rgba(248,250,252,.82) 38%,rgba(248,250,252,.18) 100%),
  url("` + loginBackgroundPath + `");background-size:cover;background-position:center;background-repeat:no-repeat}
.card{position:relative;width:min(600px,calc(100vw - 32px));box-sizing:border-box;background:rgba(255,255,255,.94);
  border:1px solid rgba(22,40,73,.08);border-radius:22px;padding:38px 74px 42px;
  box-shadow:0 22px 64px rgba(22,40,73,.14);backdrop-filter:blur(10px);-webkit-backdrop-filter:blur(10px)}
.locale-switch{position:absolute;top:18px;right:28px;display:flex;align-items:center;gap:6px;
  color:#a0aabd;font-size:12px;line-height:1}
.locale-switch a{border-radius:4px;color:#72819b;text-decoration:none;padding:4px 2px}
.locale-switch a:hover{color:#2563eb}
.locale-switch a.active{color:#2563eb;font-weight:600}
.locale-switch a:focus-visible{outline:2px solid rgba(37,99,235,.35);outline-offset:2px}
.brand{display:flex;align-items:center;justify-content:center;gap:12px;margin-bottom:20px}
.brand-logo{display:block;width:272px;height:94px;object-fit:contain}
.brand-mark{position:relative;width:40px;height:40px;border-radius:14px;
  background:linear-gradient(145deg,rgba(37,99,235,.16),rgba(37,99,235,.04)),#fff;
  box-shadow:inset 0 1px 0 rgba(255,255,255,.9),0 10px 24px rgba(37,99,235,.14)}
.brand-mark i{position:absolute;border-radius:999px}
.brand-mark .core{inset:11px;background:linear-gradient(180deg,#2563eb 0%,#1e3a8a 100%)}
.brand-mark .orbit{width:10px;height:10px;background:#f0b755;box-shadow:0 0 0 4px rgba(240,183,85,.16)}
.brand-mark .orbit-l{left:6px;top:10px}
.brand-mark .orbit-r{right:5px;bottom:7px}
.brand strong{color:#1c2438;font-size:22px;font-weight:700;letter-spacing:-.03em}
.card h3{text-align:center;font-weight:600;font-size:16px;color:#1c2438;margin:4px 0 20px}
.code{font:600 30px ui-monospace,SFMono-Regular,Menlo,monospace;letter-spacing:6px;
  text-align:center;background:#f9fbff;border:1px solid rgba(15,30,54,.08);border-radius:12px;
  padding:18px;margin:8px 0;color:#1c2438}
.label{font-size:12px;color:#72819b;text-align:center;margin-bottom:4px}
.note{font-size:13px;color:#64748d;background:#f9fbff;border:1px solid rgba(15,30,54,.08);
  border-radius:10px;padding:12px;margin:16px 0}
input{width:100%;box-sizing:border-box;background:#fff;border:1px solid #d9d9d9;border-radius:9px;
  padding:13px 14px;color:rgba(0,0,0,.88);font-size:15px;margin:7px 0;outline:none;
  transition:border-color .2s,box-shadow .2s}
input::placeholder{color:rgba(0,0,0,.35)}
input:focus{border-color:#2563eb;box-shadow:0 0 0 2px rgba(37,99,235,.1)}
ul{list-style:none;padding:0;margin:14px 0}
li{padding:6px 0;font-size:14px;color:#152239}li:before{content:"✓ ";color:#2563eb}
button,.btn{width:100%;box-sizing:border-box;border:0;border-radius:9px;padding:14px;
  font:inherit;font-size:15px;font-weight:600;cursor:pointer;margin-top:8px;
  transition:background .2s,color .2s}
.primary{background:#2563eb;color:#fff;box-shadow:0 2px 0 rgba(37,99,235,.1)}
.primary:hover{background:#1d4ed8}
.ghost{background:transparent;color:#64748d;font-weight:500}
.ghost:hover{color:#dc2626}
.err{color:#dc2626;font-size:13px;text-align:center;margin:8px 0 0}
form{width:min(464px,100%);margin:0 auto}
@media (max-width:640px){body:before{background-position:60% center}.card{padding:36px 24px}.locale-switch{right:18px}}
</style>`

//go:embed assets/openbkn-logo.svg
var openBKNLogoSVG []byte

//go:embed assets/login-background.jpg
var loginBackgroundJPG []byte

const (
	openBKNLogoPath     = "/login/assets/openbkn-logo-5175503c14ed.svg"
	openBKNLogoETag     = `"5175503c14ed4a59773aa337b07e333a632285468bba53b8bbe594ff8a5afc48"`
	loginBackgroundPath = "/login/assets/login-background-2c76122b3960.jpg"
	loginBackgroundETag = `"2c76122b39608ab1be202d1f3009ed26a09d8a0e721d4a20b297bbb449a6bb91"`
)

func serveOpenBKNLogo(c *gin.Context) {
	serveCacheableAuthAsset(c, "image/svg+xml; charset=utf-8", openBKNLogoETag, openBKNLogoSVG)
}

func serveLoginBackground(c *gin.Context) {
	serveCacheableAuthAsset(c, "image/jpeg", loginBackgroundETag, loginBackgroundJPG)
}

func serveCacheableAuthAsset(c *gin.Context, contentType, etag string, body []byte) {
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("ETag", etag)
	c.Header("X-Content-Type-Options", "nosniff")
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, contentType, body)
}

// brand renders the brand row (mark + wordmark) shown atop each card. Web
// login pages carry the BKN Studio wordmark; the device-flow pages (CLI /
// platform-level login) carry BKN Foundry.
func brand(name string) string {
	if name == "BKN Studio" {
		return `<div class="brand"><img class="brand-logo" src="` + openBKNLogoPath + `" alt="OpenBKN" width="272" height="94"></div>`
	}
	return `<div class="brand"><span class="brand-mark"><i class="core"></i><i class="orbit orbit-l"></i><i class="orbit orbit-r"></i></span><strong>` + name + `</strong></div>`
}

const authMessagePrefix = "BknSafe.Auth."

type authPageText struct {
	AccountPlaceholder         string
	PasswordPlaceholder        string
	LoginButton                string
	ChangePasswordTitle        string
	FirstLoginPrompt           string
	CurrentPasswordPlaceholder string
	NewPasswordPlaceholder     string
	ConfirmPasswordPlaceholder string
	ChangeAndLoginButton       string
	AuthorizeClient            string
	ApplicationPermissions     string
	BasicLogin                 string
	AllowButton                string
	DenyButton                 string
	DeviceAuthorizationTitle   string
	DeviceCodeLabel            string
	DeviceCodePlaceholder      string
	DeviceConfirmationNote     string
	ConfirmButton              string
	LoginSuccessTitle          string
	DeviceAuthorizedNote       string
	LanguageSelectorLabel      string
	ChineseLanguageLabel       string
	EnglishLanguageLabel       string
}

type authPageData struct {
	Challenge  string
	Account    string
	ClientName string
	Scopes     []string
	UserCode   string
	Error      string
	Language   string
	ChineseURL string
	EnglishURL string
	Text       authPageText
}

func localizedAuthMessage(c *gin.Context, key string) string {
	language := sharedrest.GetLanguageByCtx(c.Request.Context())
	return locale.Translate(language, authMessagePrefix+key)
}

func localizedAuthPageData(c *gin.Context) authPageData {
	message := func(key string) string { return localizedAuthMessage(c, key) }
	return authPageData{Language: sharedrest.GetLanguageByCtx(c.Request.Context()), Text: authPageText{
		AccountPlaceholder:         message("AccountPlaceholder"),
		PasswordPlaceholder:        message("PasswordPlaceholder"),
		LoginButton:                message("LoginButton"),
		ChangePasswordTitle:        message("ChangePasswordTitle"),
		FirstLoginPrompt:           message("FirstLoginPrompt"),
		CurrentPasswordPlaceholder: message("CurrentPasswordPlaceholder"),
		NewPasswordPlaceholder:     message("NewPasswordPlaceholder"),
		ConfirmPasswordPlaceholder: message("ConfirmPasswordPlaceholder"),
		ChangeAndLoginButton:       message("ChangeAndLoginButton"),
		AuthorizeClient:            message("AuthorizeClient"),
		ApplicationPermissions:     message("ApplicationPermissions"),
		BasicLogin:                 message("BasicLogin"),
		AllowButton:                message("AllowButton"),
		DenyButton:                 message("DenyButton"),
		DeviceAuthorizationTitle:   message("DeviceAuthorizationTitle"),
		DeviceCodeLabel:            message("DeviceCodeLabel"),
		DeviceCodePlaceholder:      message("DeviceCodePlaceholder"),
		DeviceConfirmationNote:     message("DeviceConfirmationNote"),
		ConfirmButton:              message("ConfirmButton"),
		LoginSuccessTitle:          message("LoginSuccessTitle"),
		DeviceAuthorizedNote:       message("DeviceAuthorizedNote"),
		LanguageSelectorLabel:      message("LanguageSelectorLabel"),
		ChineseLanguageLabel:       message("ChineseLanguageLabel"),
		EnglishLanguageLabel:       message("EnglishLanguageLabel"),
	}}
}

const localeSwitcher = `<nav class="locale-switch" aria-label="{{.Text.LanguageSelectorLabel}}">
  <a class="{{if eq .Language "zh-CN"}}active{{end}}" href="{{.ChineseURL}}" lang="zh-CN" hreflang="zh-CN">{{.Text.ChineseLanguageLabel}}</a>
  <span aria-hidden="true">/</span>
  <a class="{{if eq .Language "en-US"}}active{{end}}" href="{{.EnglishURL}}" lang="en-US" hreflang="en-US">{{.Text.EnglishLanguageLabel}}</a>
</nav>`

var loginPage = template.Must(template.New("login").Parse(`<!doctype html><html lang="{{.Language}}"><head><meta charset="utf-8">` + pageCSS + `</head><body>
<div class="card">` + localeSwitcher + brand("BKN Studio") + `
<form method="post" action="/login">
  <input type="hidden" name="login_challenge" value="{{.Challenge}}">
  <input type="hidden" name="lang" value="{{.Language}}">
  <input name="account" placeholder="{{.Text.AccountPlaceholder}}" value="{{.Account}}" autofocus autocomplete="username">
  <input name="password" type="password" placeholder="{{.Text.PasswordPlaceholder}}" autocomplete="current-password">
  {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
  <button class="primary" type="submit">{{.Text.LoginButton}}</button>
</form></div></body></html>`))

var changePasswordPage = template.Must(template.New("changepw").Parse(`<!doctype html><html lang="{{.Language}}"><head><meta charset="utf-8">` + pageCSS + `</head><body>
<div class="card">` + localeSwitcher + brand("BKN Studio") + `<h3>{{.Text.ChangePasswordTitle}}</h3>
<div class="label">{{.Text.FirstLoginPrompt}}</div>
{{if .Error}}<div class="note">{{.Error}}</div>{{end}}
<form method="post" action="/change-password">
  <input type="hidden" name="login_challenge" value="{{.Challenge}}">
  <input type="hidden" name="account" value="{{.Account}}">
  <input type="hidden" name="lang" value="{{.Language}}">
  <input name="old_password" type="password" placeholder="{{.Text.CurrentPasswordPlaceholder}}" autofocus autocomplete="current-password">
  <input name="new_password" type="password" placeholder="{{.Text.NewPasswordPlaceholder}}" autocomplete="new-password">
  <input name="confirm_password" type="password" placeholder="{{.Text.ConfirmPasswordPlaceholder}}" autocomplete="new-password">
  <button class="primary" type="submit">{{.Text.ChangeAndLoginButton}}</button>
</form></div></body></html>`))

var consentPage = template.Must(template.New("consent").Parse(`<!doctype html><html lang="{{.Language}}"><head><meta charset="utf-8">` + pageCSS + `</head><body>
<div class="card">` + localeSwitcher + brand("BKN Studio") + `<h3>{{.Text.AuthorizeClient}} {{.ClientName}}</h3>
<div class="label">{{.Text.ApplicationPermissions}}</div>
<ul>{{range .Scopes}}<li>{{.}}</li>{{else}}<li>{{$.Text.BasicLogin}}</li>{{end}}</ul>
<form method="post" action="/consent">
  <input type="hidden" name="consent_challenge" value="{{.Challenge}}">
  <input type="hidden" name="lang" value="{{.Language}}">
  <button class="primary" name="decision" value="allow" type="submit">{{.Text.AllowButton}}</button>
  <button class="ghost" name="decision" value="deny" type="submit">{{.Text.DenyButton}}</button>
</form></div></body></html>`))

var devicePage = template.Must(template.New("device").Parse(`<!doctype html><html lang="{{.Language}}"><head><meta charset="utf-8">` + pageCSS + `</head><body>
<div class="card">` + localeSwitcher + brand("BKN Foundry") + `<h3>{{.Text.DeviceAuthorizationTitle}}</h3>
<div class="label">{{.Text.DeviceCodeLabel}}</div>
<div class="code">{{if .UserCode}}{{.UserCode}}{{else}}— — — —{{end}}</div>
<form method="post" action="/device">
  <input type="hidden" name="device_challenge" value="{{.Challenge}}">
  <input type="hidden" name="lang" value="{{.Language}}">
  {{if not .UserCode}}<input name="user_code" placeholder="{{.Text.DeviceCodePlaceholder}}" autofocus>{{else}}<input type="hidden" name="user_code" value="{{.UserCode}}">{{end}}
  <div class="note">{{.Text.DeviceConfirmationNote}}</div>
  <button class="primary" type="submit">{{.Text.ConfirmButton}}</button>
</form></div></body></html>`))

var deviceSuccessPage = template.Must(template.New("devicesuccess").Parse(`<!doctype html><html lang="{{.Language}}"><head><meta charset="utf-8">` + pageCSS + `</head><body>
<div class="card">` + localeSwitcher + brand("BKN Foundry") + `<h3>{{.Text.LoginSuccessTitle}}</h3>
<div class="note">{{.Text.DeviceAuthorizedNote}}</div>
</div></body></html>`))

// showDeviceSuccess is hydra's URLS_DEVICE_SUCCESS target: shown after the device
// authorization is approved (the token is already issued to the CLI). Replaces
// hydra's bare fallback page. Static — no challenge needed.
func showDeviceSuccess(c *gin.Context) {
	applyAuthLocale(c, "")
	renderHTML(c, deviceSuccessPage, localizedAuthPageDataFor(c, "/device/success", nil))
}

func renderHTML(c *gin.Context, t *template.Template, data any) {
	renderHTMLStatus(c, http.StatusOK, t, data)
}

func renderHTMLStatus(c *gin.Context, status int, t *template.Template, data any) {
	c.Status(status)
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	sharedrest.MarkLocalizedCacheableResponse(c)
	_ = t.Execute(c.Writer, data)
}

func replyLocalizedAuthText(c *gin.Context, status int, messageID string) {
	language := sharedrest.GetLanguageByCtx(c.Request.Context())
	c.Header("Cache-Control", "no-store")
	sharedrest.MarkLocalizedCacheableResponse(c)
	c.String(status, locale.Translate(language, messageID))
}

func isExpiredLoginRequest(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "request_unauthorized") ||
		strings.Contains(msg, "login request has expired")
}

func showLogin(c *gin.Context, h *auth.HydraAdmin) {
	challenge := c.Query("login_challenge")
	if challenge == "" {
		applyAuthLocale(c, "")
		replyLocalizedAuthText(c, http.StatusBadRequest, authMessagePrefix+"MissingLoginChallenge")
		return
	}
	requestURL := ""
	if h != nil {
		loginRequest, err := h.GetLogin(c.Request.Context(), challenge)
		if err != nil {
			// Locale enrichment is presentation-only. A temporary Hydra read
			// failure must not make an otherwise renderable login page fail.
			slog.Debug("login: locale request lookup failed", "err", err)
		} else {
			requestURL = loginRequest.RequestURL
		}
	}
	applyAuthLocale(c, requestURL)
	data := loginAuthPageData(c, challenge)
	renderHTML(c, loginPage, data)
}

func doLogin(c *gin.Context, p *auth.Provider, accessStore *accesslog.Store) {
	applyAuthLocale(c, "")
	challenge := c.PostForm("login_challenge")
	account := c.PostForm("account")
	password := c.PostForm("password")
	if challenge == "" {
		replyLocalizedAuthText(c, http.StatusBadRequest, authMessagePrefix+"MissingLoginChallenge")
		return
	}
	redirectTo, user, err := p.Login(c.Request.Context(), challenge, account, password, false)
	if err != nil {
		if errors.Is(err, auth.ErrMustChangePassword) {
			// Credentials are valid but a password change is required first.
			// Render the change-password page directly (no server session); the
			// form re-carries the challenge + account and re-collects the old pw.
			data := changePasswordAuthPageData(c, challenge, account)
			renderHTML(c, changePasswordPage, data)
			return
		}
		if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrUserDisabled) {
			recordLogin(c, accessStore, nil, account, "failure", loginFailureCode(err))
			// Re-render the login form with an inline error instead of a bare
			// error page, keeping the entered account and the same challenge.
			data := loginAuthPageData(c, challenge)
			data.Account = account
			data.Error = localizedAuthMessage(c, "InvalidCredentials")
			renderHTMLStatus(c, http.StatusUnauthorized, loginPage, data)
			return
		}
		slog.Error("login: accept failed", "err", err)
		if isExpiredLoginRequest(err) {
			data := loginAuthPageData(c, challenge)
			data.Account = account
			data.Error = localizedAuthMessage(c, "LoginRequestExpired")
			renderHTMLStatus(c, http.StatusUnauthorized, loginPage, data)
			return
		}
		replyLocalizedAuthText(c, http.StatusInternalServerError, "BknSafe.InternalError.Description")
		return
	}
	recordLogin(c, accessStore, user, account, "success", "")
	c.Redirect(http.StatusFound, redirectTo)
}

// showChangePassword renders the change-password form. Reached via the forced
// first-login branch in doLogin (which renders it directly) or a direct GET
// carrying login_challenge + account.
func showChangePassword(c *gin.Context) {
	applyAuthLocale(c, "")
	challenge := c.Query("login_challenge")
	if challenge == "" {
		replyLocalizedAuthText(c, http.StatusBadRequest, authMessagePrefix+"MissingLoginChallenge")
		return
	}
	data := changePasswordAuthPageData(c, challenge, c.Query("account"))
	renderHTML(c, changePasswordPage, data)
}

// doChangePassword re-verifies the current password, sets the new one, and
// completes the hydra login. Validation errors re-render the page with a note.
func doChangePassword(c *gin.Context, p *auth.Provider, accessStore *accesslog.Store) {
	applyAuthLocale(c, "")
	challenge := c.PostForm("login_challenge")
	account := resolveChangePasswordAccount(c, c.PostForm("account"))
	oldPw := c.PostForm("old_password")
	newPw := c.PostForm("new_password")
	confirm := c.PostForm("confirm_password")
	if challenge == "" {
		replyLocalizedAuthText(c, http.StatusBadRequest, authMessagePrefix+"MissingLoginChallenge")
		return
	}
	reRender := func(messageKey string) {
		data := changePasswordAuthPageData(c, challenge, account)
		data.Error = localizedAuthMessage(c, messageKey)
		renderHTML(c, changePasswordPage, data)
	}
	switch {
	case newPw == "" || newPw != confirm:
		reRender("PasswordMismatch")
		return
	case newPw == oldPw:
		reRender("PasswordReuse")
		return
	}
	redirectTo, user, err := p.ChangePassword(c.Request.Context(), challenge, account, oldPw, newPw, false)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrUserDisabled) {
			recordLogin(c, accessStore, nil, account, "failure", loginFailureCode(err))
			reRender("CurrentPasswordInvalid")
			return
		}
		slog.Error("change-password: failed", "err", err)
		if isExpiredLoginRequest(err) {
			reRender("LoginRequestExpired")
			return
		}
		replyLocalizedAuthText(c, http.StatusInternalServerError, "BknSafe.InternalError.Description")
		return
	}
	recordLogin(c, accessStore, user, account, "success", "")
	clearChangePasswordAccount(c)
	c.Redirect(http.StatusFound, redirectTo)
}

func recordLogin(c *gin.Context, store *accesslog.Store, user *model.User, account, outcome, failureCode string) {
	if store == nil {
		return
	}
	actorID, actorName := "", strings.TrimSpace(account)
	if user != nil {
		actorID = user.ID
		actorName = strings.TrimSpace(user.Name)
		if actorName == "" {
			actorName = strings.TrimSpace(user.Account)
		}
	}
	if err := store.Record(c.Request.Context(), accesslog.Entry{
		ActorID: actorID, ActorNameSnapshot: actorName, AuthMethod: "password", SourceChannel: "web",
		Action: "login", Outcome: outcome, FailureCode: failureCode,
		RequestID: requestIDFromHeader(c), ClientIP: c.ClientIP(),
	}); err != nil {
		slog.Error("access login fact write failed", "outcome", outcome, "error", err)
	}
}

func loginFailureCode(err error) string {
	if errors.Is(err, auth.ErrUserDisabled) {
		return "user_disabled"
	}
	return "invalid_credentials"
}

// firstPartyClients are the platform's own seeded login entry-point clients
// (see charts/bkn-safe client-seed-job). Consent is implied for them — the
// consent screen only makes sense for third-party clients asking the user to
// share access.
var firstPartyClients = map[string]bool{
	"openbkn-studio": true,
	"openbkn-cli":    true,
	"openbkn-sdk":    true,
}

// showConsent renders the consent screen (requesting client + requested scopes
// + Authorize/Decline) for third-party clients. First-party clients are
// auto-accepted without a page, mirroring a standard first-party OAuth UX.
func showConsent(c *gin.Context, p *auth.Provider) {
	setAuthLocaleContext(c, "")
	challenge := c.Query("consent_challenge")
	if challenge == "" {
		replyLocalizedAuthText(c, http.StatusBadRequest, authMessagePrefix+"MissingConsentChallenge")
		return
	}
	cr, err := p.ConsentInfo(c.Request.Context(), challenge)
	if err != nil {
		slog.Error("consent: get failed", "err", err)
		replyLocalizedAuthText(c, http.StatusInternalServerError, "BknSafe.InternalError.Description")
		return
	}
	applyAuthContinuationLocale(c, cr.RequestURL)
	if firstPartyClients[cr.ClientID] {
		redirectTo, err := p.Consent(c.Request.Context(), challenge, c.ClientIP(), auth.ClientTypeWeb, false)
		if err != nil {
			slog.Error("consent: first-party auto-accept failed", "err", err)
			replyLocalizedAuthText(c, http.StatusInternalServerError, "BknSafe.InternalError.Description")
			return
		}
		c.Redirect(http.StatusFound, redirectTo)
		return
	}
	name := cr.ClientName
	if name == "" {
		name = cr.ClientID
	}
	data := consentAuthPageData(c, challenge)
	data.ClientName = name
	data.Scopes = cr.RequestedScope
	renderHTML(c, consentPage, data)
}

// doConsent applies the user's decision: allow -> grant scope + inject ext
// claims; deny -> reject.
func doConsent(c *gin.Context, p *auth.Provider) {
	applyAuthLocale(c, "")
	challenge := c.PostForm("consent_challenge")
	if challenge == "" {
		replyLocalizedAuthText(c, http.StatusBadRequest, authMessagePrefix+"MissingConsentChallenge")
		return
	}
	ctx := c.Request.Context()
	var redirectTo string
	var err error
	if c.PostForm("decision") == "deny" {
		redirectTo, err = p.RejectConsent(ctx, challenge)
	} else {
		redirectTo, err = p.Consent(ctx, challenge, c.ClientIP(), auth.ClientTypeWeb, false)
	}
	if err != nil {
		slog.Error("consent: decision failed", "err", err)
		replyLocalizedAuthText(c, http.StatusInternalServerError, "BknSafe.InternalError.Description")
		return
	}
	c.Redirect(http.StatusFound, redirectTo)
}

func showDevice(c *gin.Context) {
	applyAuthLocale(c, "")
	data := deviceAuthPageData(c, c.Query("device_challenge"), c.Query("user_code"))
	renderHTML(c, devicePage, data)
}

// normalizeUserCode strips separators/whitespace a user (or the prefilled
// verification_uri_complete) may include, but PRESERVES case. hydra v26 issues a
// case-SENSITIVE mixed-case user_code (e.g. "nRfpqcVx"), so uppercasing it — as
// RFC 8628 §6.1 assumes for uppercase-letter codes — makes hydra's accept lookup
// fail with "user_code session could not be found or malformed". Keep only
// alphanumerics, unchanged case.
func normalizeUserCode(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func doDevice(c *gin.Context, h *auth.HydraAdmin) {
	applyAuthLocale(c, "")
	challenge := c.PostForm("device_challenge")
	userCode := normalizeUserCode(c.PostForm("user_code"))
	if challenge == "" {
		replyLocalizedAuthText(c, http.StatusBadRequest, authMessagePrefix+"MissingDeviceChallenge")
		return
	}
	if userCode == "" {
		replyLocalizedAuthText(c, http.StatusBadRequest, authMessagePrefix+"MissingUserCode")
		return
	}
	redirectTo, err := h.AcceptUserCode(c.Request.Context(), challenge, userCode)
	if err != nil {
		replyLocalizedAuthText(c, http.StatusBadRequest, authMessagePrefix+"InvalidDeviceCode")
		return
	}
	c.Redirect(http.StatusFound, redirectTo)
}
