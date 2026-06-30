package httpapi

import (
	_ "embed"
	"encoding/base64"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"bkn-safe/internal/auth"
)

// registerAuth mounts the hydra login/consent/device provider pages. hydra is
// configured with URLS_LOGIN/CONSENT/DEVICE_VERIFICATION pointing here. The
// pages are server-rendered (no SPA), styled to match the BKN Studio console:
// device shows the user_code to confirm, consent shows the requesting client +
// requested scopes with explicit Authorize/Decline.
func registerAuth(r *gin.Engine, p *auth.Provider, h *auth.HydraAdmin) {
	r.GET("/login", showLogin)
	r.POST("/login", func(c *gin.Context) { doLogin(c, p) })
	r.GET("/change-password", showChangePassword)
	r.POST("/change-password", func(c *gin.Context) { doChangePassword(c, p) })
	r.GET("/consent", func(c *gin.Context) { showConsent(c, p) })
	r.POST("/consent", func(c *gin.Context) { doConsent(c, p) })
	r.GET("/device", showDevice)
	r.POST("/device", func(c *gin.Context) { doDevice(c, h) })
	r.GET("/device/success", showDeviceSuccess)
}

// pageCSS is the shared light shell (centered card), following the BKN Studio
// console design language: #2e68ff primary, soft radial-gradient backdrop,
// white 20px-radius card, AntD-like 8px fields/buttons, and the OpenBKN logo.
const pageCSS = `<style>
:root{color-scheme:light}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
  background:radial-gradient(circle at top left,rgba(55,114,255,.12),transparent 24%),
    radial-gradient(circle at right center,rgba(243,192,91,.12),transparent 22%),
    linear-gradient(180deg,#f7f9fc 0%,#edf1f7 100%);
  color:#152239;font:15px/1.5 "Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;
  -webkit-font-smoothing:antialiased}
.card{width:380px;box-sizing:border-box;background:#fff;border:1px solid rgba(22,40,73,.08);
  border-radius:20px;padding:36px 32px;box-shadow:0 18px 48px rgba(22,40,73,.10)}
.brand{display:flex;align-items:center;justify-content:center;gap:12px;margin-bottom:18px}
.brand-logo{display:block;width:244px;height:84px;object-fit:contain}
.brand-mark{position:relative;width:40px;height:40px;border-radius:14px;
  background:linear-gradient(145deg,rgba(46,104,255,.16),rgba(46,104,255,.04)),#fff;
  box-shadow:inset 0 1px 0 rgba(255,255,255,.9),0 10px 24px rgba(46,104,255,.14)}
.brand-mark i{position:absolute;border-radius:999px}
.brand-mark .core{inset:11px;background:linear-gradient(180deg,#2762ff 0%,#1546c7 100%)}
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
input{width:100%;box-sizing:border-box;background:#fff;border:1px solid #d9d9d9;border-radius:8px;
  padding:10px 12px;color:rgba(0,0,0,.88);font-size:14px;margin:6px 0;outline:none;
  transition:border-color .2s,box-shadow .2s}
input::placeholder{color:rgba(0,0,0,.35)}
input:focus{border-color:#2e68ff;box-shadow:0 0 0 2px rgba(46,104,255,.1)}
ul{list-style:none;padding:0;margin:14px 0}
li{padding:6px 0;font-size:14px;color:#152239}li:before{content:"✓ ";color:#2e68ff}
button,.btn{width:100%;box-sizing:border-box;border:0;border-radius:8px;padding:11px;
  font:inherit;font-size:15px;font-weight:600;cursor:pointer;margin-top:8px;
  transition:background .2s,color .2s}
.primary{background:#2e68ff;color:#fff;box-shadow:0 2px 0 rgba(46,104,255,.1)}
.primary:hover{background:#4d80ff}
.ghost{background:transparent;color:#64748d;font-weight:500}
.ghost:hover{color:#dc2626}
.err{color:#dc2626;font-size:13px;text-align:center;margin:8px 0 0}
form{margin:0}
</style>`

//go:embed assets/openbkn-logo.png
var openBKNLogoPNG []byte

var openBKNLogoDataURI = "data:image/png;base64," + base64.StdEncoding.EncodeToString(openBKNLogoPNG)

// brand renders the brand row (mark + wordmark) shown atop each card. Web
// login pages carry the BKN Studio wordmark; the device-flow pages (CLI /
// platform-level login) carry BKN Foundry.
func brand(name string) string {
	if name == "BKN Studio" {
		return `<div class="brand"><img class="brand-logo" src="` + openBKNLogoDataURI + `" alt="OpenBKN"></div>`
	}
	return `<div class="brand"><span class="brand-mark"><i class="core"></i><i class="orbit orbit-l"></i><i class="orbit orbit-r"></i></span><strong>` + name + `</strong></div>`
}

var loginPage = template.Must(template.New("login").Parse(pageCSS + `<!doctype html><meta charset="utf-8"><body>
<div class="card">` + brand("BKN Studio") + `
<form method="post" action="/login">
  <input type="hidden" name="login_challenge" value="{{.Challenge}}">
  <input name="account" placeholder="账号" value="{{.Account}}" autofocus autocomplete="username">
  <input name="password" type="password" placeholder="密码" autocomplete="current-password">
  {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
  <button class="primary" type="submit">登录</button>
</form></div></body>`))

var changePasswordPage = template.Must(template.New("changepw").Parse(pageCSS + `<!doctype html><meta charset="utf-8"><body>
<div class="card">` + brand("BKN Studio") + `<h3>修改密码</h3>
<div class="label">首次登录请设置新密码</div>
{{if .Error}}<div class="note">{{.Error}}</div>{{end}}
<form method="post" action="/change-password">
  <input type="hidden" name="login_challenge" value="{{.Challenge}}">
  <input type="hidden" name="account" value="{{.Account}}">
  <input name="old_password" type="password" placeholder="当前密码" autofocus autocomplete="current-password">
  <input name="new_password" type="password" placeholder="新密码" autocomplete="new-password">
  <input name="confirm_password" type="password" placeholder="确认新密码" autocomplete="new-password">
  <button class="primary" type="submit">修改并登录</button>
</form></div></body>`))

var consentPage = template.Must(template.New("consent").Parse(pageCSS + `<!doctype html><meta charset="utf-8"><body>
<div class="card">` + brand("BKN Studio") + `<h3>授权 {{.ClientName}}</h3>
<div class="label">该应用将获得以下权限</div>
<ul>{{range .Scopes}}<li>{{.}}</li>{{else}}<li>基础登录</li>{{end}}</ul>
<form method="post" action="/consent">
  <input type="hidden" name="consent_challenge" value="{{.Challenge}}">
  <button class="primary" name="decision" value="allow" type="submit">同意授权</button>
  <button class="ghost" name="decision" value="deny" type="submit">拒绝</button>
</form></div></body>`))

var devicePage = template.Must(template.New("device").Parse(pageCSS + `<!doctype html><meta charset="utf-8"><body>
<div class="card">` + brand("BKN Foundry") + `<h3>设备授权</h3>
<div class="label">设备码</div>
<div class="code">{{if .UserCode}}{{.UserCode}}{{else}}— — — —{{end}}</div>
<form method="post" action="/device">
  <input type="hidden" name="device_challenge" value="{{.Challenge}}">
  {{if not .UserCode}}<input name="user_code" placeholder="输入设备码" autofocus>{{else}}<input type="hidden" name="user_code" value="{{.UserCode}}">{{end}}
  <div class="note">仅当你正从该设备发起登录、且设备码一致时才继续;否则请关闭本页。</div>
  <button class="primary" type="submit">确认</button>
</form></div></body>`))

var deviceSuccessPage = template.Must(template.New("devicesuccess").Parse(pageCSS + `<!doctype html><meta charset="utf-8"><body>
<div class="card">` + brand("BKN Foundry") + `<h3>登录成功</h3>
<div class="note">设备已授权,可关闭此页面,返回命令行继续。</div>
</div></body>`))

// showDeviceSuccess is hydra's URLS_DEVICE_SUCCESS target: shown after the device
// authorization is approved (the token is already issued to the CLI). Replaces
// hydra's bare fallback page. Static — no challenge needed.
func showDeviceSuccess(c *gin.Context) { renderHTML(c, deviceSuccessPage, nil) }

func renderHTML(c *gin.Context, t *template.Template, data any) {
	c.Status(http.StatusOK)
	c.Header("Content-Type", "text/html; charset=utf-8")
	_ = t.Execute(c.Writer, data)
}

func showLogin(c *gin.Context) {
	challenge := c.Query("login_challenge")
	if challenge == "" {
		c.String(http.StatusBadRequest, "missing login_challenge")
		return
	}
	renderHTML(c, loginPage, map[string]any{"Challenge": challenge})
}

func doLogin(c *gin.Context, p *auth.Provider) {
	challenge := c.PostForm("login_challenge")
	account := c.PostForm("account")
	password := c.PostForm("password")
	if challenge == "" {
		c.String(http.StatusBadRequest, "missing login_challenge")
		return
	}
	redirectTo, err := p.Login(c.Request.Context(), challenge, account, password, false)
	if err != nil {
		if errors.Is(err, auth.ErrMustChangePassword) {
			// Credentials are valid but a password change is required first.
			// Render the change-password page directly (no server session); the
			// form re-carries the challenge + account and re-collects the old pw.
			renderHTML(c, changePasswordPage, map[string]any{"Challenge": challenge, "Account": account})
			return
		}
		if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrUserDisabled) {
			// Re-render the login form with an inline error instead of a bare
			// error page, keeping the entered account and the same challenge.
			c.Status(http.StatusUnauthorized)
			c.Header("Content-Type", "text/html; charset=utf-8")
			_ = loginPage.Execute(c.Writer, map[string]any{"Challenge": challenge, "Account": account, "Error": "账号或密码错误"})
			return
		}
		slog.Error("login: accept failed", "err", err)
		c.String(http.StatusInternalServerError, "internal error")
		return
	}
	c.Redirect(http.StatusFound, redirectTo)
}

// showChangePassword renders the change-password form. Reached via the forced
// first-login branch in doLogin (which renders it directly) or a direct GET
// carrying login_challenge + account.
func showChangePassword(c *gin.Context) {
	challenge := c.Query("login_challenge")
	if challenge == "" {
		c.String(http.StatusBadRequest, "missing login_challenge")
		return
	}
	renderHTML(c, changePasswordPage, map[string]any{"Challenge": challenge, "Account": c.Query("account")})
}

// doChangePassword re-verifies the current password, sets the new one, and
// completes the hydra login. Validation errors re-render the page with a note.
func doChangePassword(c *gin.Context, p *auth.Provider) {
	challenge := c.PostForm("login_challenge")
	account := c.PostForm("account")
	oldPw := c.PostForm("old_password")
	newPw := c.PostForm("new_password")
	confirm := c.PostForm("confirm_password")
	if challenge == "" {
		c.String(http.StatusBadRequest, "missing login_challenge")
		return
	}
	reRender := func(msg string) {
		renderHTML(c, changePasswordPage, map[string]any{"Challenge": challenge, "Account": account, "Error": msg})
	}
	switch {
	case newPw == "" || newPw != confirm:
		reRender("两次输入的新密码不一致或为空")
		return
	case newPw == oldPw:
		reRender("新密码不能与当前密码相同")
		return
	}
	redirectTo, err := p.ChangePassword(c.Request.Context(), challenge, account, oldPw, newPw, false)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrUserDisabled) {
			reRender("当前密码错误")
			return
		}
		slog.Error("change-password: failed", "err", err)
		c.String(http.StatusInternalServerError, "internal error")
		return
	}
	c.Redirect(http.StatusFound, redirectTo)
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
	challenge := c.Query("consent_challenge")
	if challenge == "" {
		c.String(http.StatusBadRequest, "missing consent_challenge")
		return
	}
	cr, err := p.ConsentInfo(c.Request.Context(), challenge)
	if err != nil {
		slog.Error("consent: get failed", "err", err)
		c.String(http.StatusInternalServerError, "internal error")
		return
	}
	if firstPartyClients[cr.ClientID] {
		redirectTo, err := p.Consent(c.Request.Context(), challenge, c.ClientIP(), auth.ClientTypeWeb, false)
		if err != nil {
			slog.Error("consent: first-party auto-accept failed", "err", err)
			c.String(http.StatusInternalServerError, "internal error")
			return
		}
		c.Redirect(http.StatusFound, redirectTo)
		return
	}
	name := cr.ClientName
	if name == "" {
		name = cr.ClientID
	}
	renderHTML(c, consentPage, map[string]any{"Challenge": challenge, "ClientName": name, "Scopes": cr.RequestedScope})
}

// doConsent applies the user's decision: allow -> grant scope + inject ext
// claims; deny -> reject.
func doConsent(c *gin.Context, p *auth.Provider) {
	challenge := c.PostForm("consent_challenge")
	if challenge == "" {
		c.String(http.StatusBadRequest, "missing consent_challenge")
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
		c.String(http.StatusInternalServerError, "internal error")
		return
	}
	c.Redirect(http.StatusFound, redirectTo)
}

func showDevice(c *gin.Context) {
	renderHTML(c, devicePage, map[string]string{
		"Challenge": c.Query("device_challenge"),
		"UserCode":  c.Query("user_code"), // prefilled from verification_uri_complete
	})
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
	challenge := c.PostForm("device_challenge")
	userCode := normalizeUserCode(c.PostForm("user_code"))
	if challenge == "" || userCode == "" {
		c.String(http.StatusBadRequest, "missing device_challenge or user_code")
		return
	}
	redirectTo, err := h.AcceptUserCode(c.Request.Context(), challenge, userCode)
	if err != nil {
		c.String(http.StatusBadRequest, "无效的设备码")
		return
	}
	c.Redirect(http.StatusFound, redirectTo)
}
