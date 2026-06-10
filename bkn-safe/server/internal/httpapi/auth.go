package httpapi

import (
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
// pages are server-rendered (no SPA), styled after a standard OAuth consent UX:
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

// page is the shared dark-theme shell (centered card), echoing a clean OAuth UX.
const pageCSS = `<style>
:root{color-scheme:dark}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
  background:#1f1f1d;color:#e8e6e1;font:15px/1.5 -apple-system,Segoe UI,Roboto,sans-serif}
.card{width:360px;background:#262624;border:1px solid #3a3a37;border-radius:16px;padding:32px}
.card h3{text-align:center;font-weight:600;margin:8px 0 20px}
.code{font:600 30px ui-monospace,SFMono-Regular,Menlo,monospace;letter-spacing:6px;
  text-align:center;background:#1f1f1d;border:1px solid #3a3a37;border-radius:12px;padding:18px;margin:8px 0}
.label{font-size:12px;color:#a3a098;text-align:center;margin-bottom:4px}
.note{font-size:13px;color:#a3a098;background:#1f1f1d;border:1px solid #3a3a37;border-radius:10px;padding:12px;margin:16px 0}
input{width:100%;box-sizing:border-box;background:#1f1f1d;border:1px solid #3a3a37;border-radius:10px;
  padding:11px 13px;color:#e8e6e1;font-size:14px;margin:6px 0}
ul{list-style:none;padding:0;margin:14px 0}
li{padding:6px 0;font-size:14px}li:before{content:"✓ ";color:#c9a8;color:#cf9a6b}
button,.btn{width:100%;box-sizing:border-box;border:0;border-radius:10px;padding:12px;
  font-size:15px;font-weight:600;cursor:pointer;margin-top:8px}
.primary{background:#e8e6e1;color:#1f1f1d}
.ghost{background:transparent;color:#a3a098;font-weight:500}
.err{color:#e5736d;font-size:13px;text-align:center;margin:8px 0 0}
form{margin:0}
</style>`

var loginPage = template.Must(template.New("login").Parse(pageCSS + `<!doctype html><meta charset="utf-8"><body>
<div class="card"><h3>BKN Foundry 登录</h3>
<form method="post" action="/login">
  <input type="hidden" name="login_challenge" value="{{.Challenge}}">
  <input name="account" placeholder="账号" value="{{.Account}}" autofocus autocomplete="username">
  <input name="password" type="password" placeholder="密码" autocomplete="current-password">
  {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
  <button class="primary" type="submit">登录</button>
</form></div></body>`))

var changePasswordPage = template.Must(template.New("changepw").Parse(pageCSS + `<!doctype html><meta charset="utf-8"><body>
<div class="card"><h3>修改密码</h3>
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
<div class="card"><h3>授权 {{.ClientName}}</h3>
<div class="label">该应用将获得以下权限</div>
<ul>{{range .Scopes}}<li>{{.}}</li>{{else}}<li>基础登录</li>{{end}}</ul>
<form method="post" action="/consent">
  <input type="hidden" name="consent_challenge" value="{{.Challenge}}">
  <button class="primary" name="decision" value="allow" type="submit">同意授权</button>
  <button class="ghost" name="decision" value="deny" type="submit">拒绝</button>
</form></div></body>`))

var devicePage = template.Must(template.New("device").Parse(pageCSS + `<!doctype html><meta charset="utf-8"><body>
<div class="card"><h3>设备授权</h3>
<div class="label">设备码</div>
<div class="code">{{if .UserCode}}{{.UserCode}}{{else}}— — — —{{end}}</div>
<form method="post" action="/device">
  <input type="hidden" name="device_challenge" value="{{.Challenge}}">
  {{if not .UserCode}}<input name="user_code" placeholder="输入设备码" autofocus>{{else}}<input type="hidden" name="user_code" value="{{.UserCode}}">{{end}}
  <div class="note">仅当你正从该设备发起登录、且设备码一致时才继续;否则请关闭本页。</div>
  <button class="primary" type="submit">确认</button>
</form></div></body>`))

var deviceSuccessPage = template.Must(template.New("devicesuccess").Parse(pageCSS + `<!doctype html><meta charset="utf-8"><body>
<div class="card"><h3>登录成功</h3>
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
