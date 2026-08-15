// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package hydra

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

// VisitorType identifies the visitor type.
type VisitorType string

// Visitor type values.
const (
	VisitorType_RealName  VisitorType = "realname"  // Authenticated user.
	VisitorType_User      VisitorType = "user"      // Authenticated user.
	VisitorType_Anonymous VisitorType = "anonymous" // Anonymous user.
	VisitorType_App       VisitorType = "app"       // Application account.
)

// AccountType identifies the login account type.
type AccountType string

// Account type values.
const (
	AccountType_Other  AccountType = "other"
	AccountType_IDCard AccountType = "id_card"
)

// ClientType identifies the client device type.
type ClientType string

// Client device type values.
const (
	ClientType_Windows      ClientType = "windows"
	ClientType_IOS          ClientType = "ios"
	ClientType_Android      ClientType = "android"
	ClientType_Harmony      ClientType = "harmony"
	ClientType_MacOS        ClientType = "mac_os"
	ClientType_Web          ClientType = "web"
	ClientType_MobileWeb    ClientType = "mobile_web"
	ClientType_Linux        ClientType = "linux"
	ClientType_OfficePlugin ClientType = "office_plugin"
	ClientType_ConsoleWeb   ClientType = "console_web"
	ClientType_DeployWeb    ClientType = "deploy_web"
	ClientType_Unknown      ClientType = "unknown"
	ClientType_App          ClientType = "app"
)

// TokenIntrospectInfo contains the token introspection result.
type TokenIntrospectInfo struct {
	Active     bool        // Token status.
	VisitorID  string      // Visitor ID.
	Scope      string      // Granted scopes.
	ClientID   string      // Client ID.
	VisitorTyp VisitorType // Visitor type.
	// The following fields exist only for authenticated users.
	LoginIP    string      // Login IP address.
	Udid       string      // Device ID.
	AccountTyp AccountType // Account type.
	ClientTyp  ClientType  // Client device type.
}

var (
	visitorTypeMap = map[VisitorType]VisitorType{
		VisitorType_User:      VisitorType_User,
		VisitorType_RealName:  VisitorType_User,
		VisitorType_Anonymous: VisitorType_Anonymous,
		VisitorType_App:       VisitorType_App,
	}
	accountTypeMap = map[AccountType]AccountType{
		AccountType_Other:  AccountType_Other,
		AccountType_IDCard: AccountType_IDCard,
	}
	clientTypeMap = map[ClientType]ClientType{
		ClientType_Windows:      ClientType_Windows,
		ClientType_IOS:          ClientType_IOS,
		ClientType_Android:      ClientType_Android,
		ClientType_Harmony:      ClientType_Harmony,
		ClientType_MacOS:        ClientType_MacOS,
		ClientType_Web:          ClientType_Web,
		ClientType_MobileWeb:    ClientType_MobileWeb,
		ClientType_Linux:        ClientType_Linux,
		ClientType_OfficePlugin: ClientType_OfficePlugin,
		ClientType_ConsoleWeb:   ClientType_ConsoleWeb,
		ClientType_DeployWeb:    ClientType_DeployWeb,
		ClientType_Unknown:      ClientType_Unknown,
		ClientType_App:          ClientType_App,
	}
)

// Visitor contains caller identity information.
type Visitor struct {
	ID string

	// TokenID is ignored during JSON serialization to prevent token persistence.
	// Handle TokenID explicitly when it is required after deserialization.
	TokenID    string `json:"-"`
	IP         string
	Mac        string
	UserAgent  string
	ClientID   string
	Type       VisitorType
	ClientType ClientType
}

//go:generate mockgen -package mock -source ./hydra.go -destination ./mock/mock_hydra.go

// Hydra defines the authorization service interface.
type Hydra interface {
	// Introspect performs token introspection.
	Introspect(ctx context.Context, token string) (info TokenIntrospectInfo, err error)

	// VerifyToken verifies token validity.
	VerifyToken(ctx context.Context, c *gin.Context) (Visitor, error)
}

type HydraAdminSetting struct {
	HydraAdminProcotol string
	HydraAdminHost     string
	HydraAdminPort     int
}

type hydra struct {
	adminAddress string
	client       *http.Client
}

// NewHydra creates an authorization service client.
func NewHydra(setting HydraAdminSetting) Hydra {
	h := &hydra{
		adminAddress: fmt.Sprintf("http://%s:%d", setting.HydraAdminHost, setting.HydraAdminPort),
		client:       rest.NewRawHTTPClient(),
	}

	return h
}

// Introspect performs token introspection.
func (h *hydra) Introspect(ctx context.Context, token string) (info TokenIntrospectInfo, err error) {
	url := fmt.Sprintf("%v/admin/oauth2/introspect", h.adminAddress)

	resp, err := h.client.Post(url, "application/x-www-form-urlencoded",
		bytes.NewReader([]byte(fmt.Sprintf("token=%v", token))))
	if err != nil {
		logger.Error(err)
		return
	}

	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			logger.Error(closeErr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if (resp.StatusCode < http.StatusOK) || (resp.StatusCode >= http.StatusMultipleChoices) {
		err = errors.New(string(body))
		return
	}

	respParam := make(map[string]interface{})
	err = sonic.Unmarshal(body, &respParam)
	if err != nil {
		return
	}

	// Token status.
	info.Active = respParam["active"].(bool)
	if !info.Active {
		return
	}

	// Visitor ID.
	info.VisitorID = respParam["sub"].(string)
	// Granted scopes.
	info.Scope = respParam["scope"].(string)
	// Client ID.
	info.ClientID = respParam["client_id"].(string)
	// Client-credentials grant.
	if info.VisitorID == info.ClientID {
		info.VisitorTyp = VisitorType_App
		return
	}

	// The following fields do not exist for client-credentials grants.
	// Visitor type.
	visitorTyp := respParam["ext"].(map[string]interface{})["visitor_type"].(string)
	info.VisitorTyp = visitorTypeMap[VisitorType(visitorTyp)]

	// Anonymous user.
	if info.VisitorTyp == VisitorType_Anonymous {
		// The document-library authorization API requires clientType for extensibility.
		// Anonymous callers do not provide a device type, so default to web.
		info.ClientTyp = ClientType_Web
		return
	}

	// Authenticated user.
	if info.VisitorTyp == VisitorType_User {
		// Login IP address.
		info.LoginIP = respParam["ext"].(map[string]interface{})["login_ip"].(string)
		// Device ID.
		info.Udid = respParam["ext"].(map[string]interface{})["udid"].(string)
		// Login account type.
		accountTyp := respParam["ext"].(map[string]interface{})["account_type"].(string)
		info.AccountTyp = accountTypeMap[AccountType(accountTyp)]
		// Client device type.
		clientTyp := respParam["ext"].(map[string]interface{})["client_type"].(string)
		info.ClientTyp = clientTypeMap[ClientType(clientTyp)]
		return
	}

	return
}

func (h *hydra) VerifyToken(ctx context.Context, c *gin.Context) (Visitor, error) {
	tokenID := c.GetHeader("Authorization")
	token := strings.TrimPrefix(tokenID, "Bearer ")
	info, err := h.Introspect(ctx, token)
	if err != nil {
		return Visitor{}, err
	}

	if !info.Active {
		err = errors.New("oauth info is not active")
		return Visitor{}, err
	}

	visitor := Visitor{
		ID:         info.VisitorID,
		TokenID:    tokenID,
		IP:         c.ClientIP(),
		Mac:        c.GetHeader("X-Request-MAC"),
		UserAgent:  c.GetHeader("User-Agent"),
		Type:       info.VisitorTyp,
		ClientType: info.ClientTyp,
		ClientID:   info.ClientID,
	}

	return visitor, nil
}
