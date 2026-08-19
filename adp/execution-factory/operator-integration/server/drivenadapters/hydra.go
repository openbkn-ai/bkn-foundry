// Package drivenadapters defines driver adapters.
// @file hydra.go
// @description: Implement the authorization service interface.
package drivenadapters

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	jsoniter "github.com/json-iterator/go"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
)

type hydraService struct {
	adminAddress string
	logger       interfaces.Logger
	httpClient   interfaces.HTTPClient
}

type noopHydra struct{}

var (
	once sync.Once
	h    interfaces.Hydra
)

// Extend parses extended information.
type Extend struct {
	AccountType string `json:"account_type"`
	ClientType  string `json:"client_type"`
	LoginIP     string `json:"login_ip"`
	UdID        string `json:"udid"`
	VisitorType string `json:"visitor_type"`
	PhoneNumber string `json:"phone_number"`
	VisitorName string `json:"visitor_name"`
}

// IntrospectInfo introspection information.
type IntrospectInfo struct {
	Active    bool   `json:"active"`
	Scope     string `json:"scope"`
	ClientID  string `json:"client_id"`
	SubID     string `json:"sub"`
	TokenType string `json:"token_type"`
	Ext       Extend `json:"ext"`
}

const introspectURI = "/oauth2/introspect"

// NewHydra creates an authorization service object.
func NewHydra() interfaces.Hydra {
	if !config.GetAuthEnabled() {
		return &noopHydra{}
	}
	once.Do(func() {
		config := config.NewConfigLoader()
		h = &hydraService{
			adminAddress: fmt.Sprintf("http://%s:%d%s", config.OAuth.AdminHost, config.OAuth.AdminPort, config.OAuth.AdminPrefix),
			logger:       config.GetLogger(),
			httpClient:   rest.NewHTTPClient(),
		}
	})
	return h
}

// Get common authentication information.
// Get X-Account-Type and X-Account-ID from Header and build TokenInfo object.
// If X-Account-Type is empty, the default setting is AccessorTypeAnonymous.
// If X-Account-ID is empty, the default setting is the empty string.

func (n *noopHydra) GenerateVisitor(c *gin.Context) (info *interfaces.TokenInfo, err error) {
	xAccountType := c.GetHeader(string(interfaces.HeaderXAccountType))
	xAccountID := c.GetHeader(string(interfaces.HeaderXAccountID))
	if xAccountID == "" {
		// If the user is not logged in, the default is set to Administrator.
		xAccountID = interfaces.ADMIN_ACCOUNT_ID
		xAccountType = interfaces.ADMIN_ACCOUNT_TYPE
	}
	info = &interfaces.TokenInfo{
		Active:     true,
		VisitorID:  xAccountID,
		VisitorTyp: interfaces.AccessorType(xAccountType).ToVisitorType(),
		LoginIP:    c.ClientIP(),
		MAC:        c.GetHeader("X-Request-MAC"),
		UserAgent:  c.GetHeader("User-Agent"),
	}

	return info, nil
}

func (n *noopHydra) Introspect(c *gin.Context) (info *interfaces.TokenInfo, err error) {
	info, err = n.GenerateVisitor(c)
	return
}

func (h *hydraService) GenerateVisitor(c *gin.Context) (info *interfaces.TokenInfo, err error) {
	xAccountType := c.GetHeader(string(interfaces.HeaderXAccountType))
	xAccountID := c.GetHeader(string(interfaces.HeaderXAccountID))
	info = &interfaces.TokenInfo{
		Active:     true,
		VisitorID:  xAccountID,
		VisitorTyp: interfaces.AccessorType(xAccountType).ToVisitorType(),
		LoginIP:    c.ClientIP(),
		MAC:        c.GetHeader("X-Request-MAC"),
		UserAgent:  c.GetHeader("User-Agent"),
	}
	return info, nil
}

// Introspect tokenIntrospection.
func (h *hydraService) Introspect(c *gin.Context) (info *interfaces.TokenInfo, err error) {
	ctx := c.Request.Context()
	token := GetToken(c)
	target := fmt.Sprintf("%s%s", h.adminAddress, introspectURI)
	header := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	_, resp, err := h.httpClient.Post(ctx, target, header, []byte(fmt.Sprintf("token=%v", token)))
	if err != nil {
		h.logger.WithContext(ctx).Error(err)
		return
	}
	introspectInfo := &IntrospectInfo{}
	respByt := utils.ObjectToByte(resp)
	if err = jsoniter.Unmarshal(respByt, introspectInfo); err != nil {
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		h.logger.WithContext(ctx).Warnf("Get introspect object to struct failed:%+v, resp:%+v", err, resp)
		return
	}
	info = &interfaces.TokenInfo{}
	// Token status.
	info.Active = introspectInfo.Active
	if !info.Active {
		err = errors.DefaultHTTPError(ctx, http.StatusUnauthorized, "token is invalid")
		return
	}
	// Visitor ID.
	info.VisitorID = introspectInfo.SubID
	// Scope scope of authority.
	info.Scope = introspectInfo.Scope
	// Client ID.
	info.ClientID = introspectInfo.ClientID
	// Client Credentials Mode.
	if info.VisitorID == info.ClientID {
		info.VisitorTyp = interfaces.Business
		return
	}
	// The following fields are only present in non-client credentials mode.
	// Visitor type.
	info.VisitorTyp = interfaces.VisitorType(introspectInfo.Ext.VisitorType)

	// anonymous user.
	if info.VisitorTyp == interfaces.Anonymous {
		info.PhoneNumber = introspectInfo.Ext.PhoneNumber
		info.VisitorName = introspectInfo.Ext.VisitorName
		return
	}
	// Real-name user.
	if info.VisitorTyp == interfaces.RealName {
		// Login IP.
		info.LoginIP = introspectInfo.Ext.LoginIP
		// Username.
		info.VisitorName = introspectInfo.Ext.VisitorName
		// Device ID.
		info.Udid = introspectInfo.Ext.UdID
		// Login account type.
		info.AccountTyp = interfaces.ReverseAccountTypeMap[introspectInfo.Ext.AccountType]
		// Device type.
		info.ClientTyp = interfaces.ReverseClientTypeMap[introspectInfo.Ext.ClientType]
	}
	if info.LoginIP == "" {
		// If the returned IP is empty, use clientIP.
		info.LoginIP = c.ClientIP()
	}
	info.MAC = c.GetHeader("X-Request-MAC")
	info.UserAgent = c.GetHeader("User-Agent")
	return
}

// GetToken extracts the credentials from the request and tries the Authorization and X-Authorization headers in sequence.
// If both are empty, fall back to the token query parameter. It is used by the public authentication middleware to determine the credential type (see interfaces.AppKeyPrefix).
func GetToken(c *gin.Context) (token string) {
	tokenID := c.GetHeader("Authorization")
	if tokenID == "" {
		tokenID = c.GetHeader("X-Authorization")
	}
	if tokenID == "" {
		token, _ = c.GetQuery("token")
	} else {
		token = strings.TrimPrefix(tokenID, "Bearer ")
	}
	return token
}
