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

	"github.com/openbkn-ai/bkn-comm-go/logger"
	"github.com/openbkn-ai/bkn-comm-go/rest"
)

// VisitorType 访问者类型
type VisitorType string

// 访问者类型定义
const (
	VisitorType_RealName  VisitorType = "realname"  // 实名用户
	VisitorType_User      VisitorType = "user"      // 实名用户
	VisitorType_Anonymous VisitorType = "anonymous" // 匿名用户
	VisitorType_App       VisitorType = "app"       // 应用账户
)

// AccountType 登录账号类型
type AccountType string

// 登录账号类型定义
const (
	AccountType_Other  AccountType = "other"
	AccountType_IDCard AccountType = "id_card"
)

// ClientType 设备类型
type ClientType string

// 设备类型定义
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

// TokenIntrospectInfo 令牌内省结果
type TokenIntrospectInfo struct {
	Active     bool        // 令牌状态
	VisitorID  string      // 访问者ID
	Scope      string      // 权限范围
	ClientID   string      // 客户端ID
	VisitorTyp VisitorType // 访问者类型
	// 以下字段只在visitorType=RealName，即实名用户时才存在
	LoginIP    string      // 登陆IP
	Udid       string      // 设备码
	AccountTyp AccountType // 账户类型
	ClientTyp  ClientType  // 设备类型
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

// Visitor 访问者信息
type Visitor struct {
	ID string

	// TokenID 在 JSON 序列化和反序列化时会被忽略，用于防止令牌在持久化过程中泄露
	// 如需在反序列化时获取 TokenID，请通过代码手动处理
	TokenID    string `json:"-"`
	IP         string
	Mac        string
	UserAgent  string
	ClientID   string
	Type       VisitorType
	ClientType ClientType
}

//go:generate mockgen -package mock -source ./hydra.go -destination ./mock/mock_hydra.go

// Hydra 授权服务接口
type Hydra interface {
	// Introspect token内省
	Introspect(ctx context.Context, token string) (info TokenIntrospectInfo, err error)

	// token 有效性检查
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

// newHydra 创建授权服务
func NewHydra(setting HydraAdminSetting) Hydra {
	h := &hydra{
		adminAddress: fmt.Sprintf("http://%s:%d", setting.HydraAdminHost, setting.HydraAdminPort),
		client:       rest.NewRawHTTPClient(),
	}

	return h
}

// Introspect token内省
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

	// 令牌状态
	info.Active = respParam["active"].(bool)
	if !info.Active {
		return
	}

	// 访问者ID
	info.VisitorID = respParam["sub"].(string)
	// Scope 权限范围
	info.Scope = respParam["scope"].(string)
	// 客户端ID
	info.ClientID = respParam["client_id"].(string)
	// 客户端凭据模式
	if info.VisitorID == info.ClientID {
		info.VisitorTyp = VisitorType_App
		return
	}

	// 以下字段 只在非客户端凭据模式时才存在
	// 访问者类型
	visitorTyp := respParam["ext"].(map[string]interface{})["visitor_type"].(string)
	info.VisitorTyp = visitorTypeMap[VisitorType(visitorTyp)]

	// 匿名用户
	if info.VisitorTyp == VisitorType_Anonymous {
		// 文档库访问规则接口考虑后续扩展性，clientType为必传。本身规则计算未使用clientType
		// 设备类型本身未解析,匿名时默认为web
		info.ClientTyp = ClientType_Web
		return
	}

	// 实名用户
	if info.VisitorTyp == VisitorType_User {
		// 登陆IP
		info.LoginIP = respParam["ext"].(map[string]interface{})["login_ip"].(string)
		// 设备ID
		info.Udid = respParam["ext"].(map[string]interface{})["udid"].(string)
		// 登录账号类型
		accountTyp := respParam["ext"].(map[string]interface{})["account_type"].(string)
		info.AccountTyp = accountTypeMap[AccountType(accountTyp)]
		// 设备类型
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
