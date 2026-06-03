package httpapi

import (
	"errors"
	"html/template"
	"net/http"

	"github.com/gin-gonic/gin"

	"bkn-safe/internal/auth"
)

// registerAuth mounts the hydra login/consent/device provider pages. hydra is
// configured with URLS_LOGIN/CONSENT/DEVICE_VERIFICATION pointing here.
func registerAuth(r *gin.Engine, p *auth.Provider, h *auth.HydraAdmin) {
	r.GET("/login", showLogin)
	r.POST("/login", func(c *gin.Context) { doLogin(c, p) })
	r.GET("/consent", func(c *gin.Context) { doConsent(c, p) })
	r.GET("/device", showDevice)
	r.POST("/device", func(c *gin.Context) { doDevice(c, h) })
}

// loginPage is a minimal first-party login form. A richer SPA can replace it;
// the contract is only the POST below.
var loginPage = template.Must(template.New("login").Parse(`<!doctype html><html><body>
<h3>bkn-safe 登录</h3>
<form method="post" action="/login">
  <input type="hidden" name="login_challenge" value="{{.Challenge}}">
  <p><input name="account" placeholder="账号" autofocus></p>
  <p><input name="password" type="password" placeholder="密码"></p>
  <p><button type="submit">登录</button></p>
</form></body></html>`))

func showLogin(c *gin.Context) {
	challenge := c.Query("login_challenge")
	if challenge == "" {
		c.String(http.StatusBadRequest, "missing login_challenge")
		return
	}
	c.Status(http.StatusOK)
	c.Header("Content-Type", "text/html; charset=utf-8")
	_ = loginPage.Execute(c.Writer, map[string]string{"Challenge": challenge})
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
		if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrUserDisabled) {
			c.String(http.StatusUnauthorized, "登录失败：账号或密码错误")
			return
		}
		c.String(http.StatusInternalServerError, "internal error")
		return
	}
	c.Redirect(http.StatusFound, redirectTo)
}

// doConsent auto-grants the requested scope for first-party clients and injects
// the introspect ext claims. (A consent screen can be added later; Kowell's
// first-party flow does not need an interactive grant.)
func doConsent(c *gin.Context, p *auth.Provider) {
	challenge := c.Query("consent_challenge")
	if challenge == "" {
		c.String(http.StatusBadRequest, "missing consent_challenge")
		return
	}
	redirectTo, err := p.Consent(c.Request.Context(), challenge, c.ClientIP(), auth.ClientTypeWeb, false)
	if err != nil {
		c.String(http.StatusInternalServerError, "internal error")
		return
	}
	c.Redirect(http.StatusFound, redirectTo)
}

var devicePage = template.Must(template.New("device").Parse(`<!doctype html><html><body>
<h3>设备授权</h3>
<form method="post" action="/device">
  <input type="hidden" name="device_challenge" value="{{.Challenge}}">
  <p><input name="user_code" placeholder="输入设备码" value="{{.UserCode}}" autofocus></p>
  <p><button type="submit">确认</button></p>
</form></body></html>`))

func showDevice(c *gin.Context) {
	c.Status(http.StatusOK)
	c.Header("Content-Type", "text/html; charset=utf-8")
	_ = devicePage.Execute(c.Writer, map[string]string{
		"Challenge": c.Query("device_challenge"),
		"UserCode":  c.Query("user_code"), // prefilled from verification_uri_complete
	})
}

func doDevice(c *gin.Context, h *auth.HydraAdmin) {
	challenge := c.PostForm("device_challenge")
	userCode := c.PostForm("user_code")
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
