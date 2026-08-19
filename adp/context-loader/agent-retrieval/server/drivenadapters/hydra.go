// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package drivenadapters

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	jsoniter "github.com/json-iterator/go"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/utils"
)

type hydra struct {
	adminAddress string
	logger       interfaces.Logger
	httpClient   interfaces.HTTPClient
}

var (
	once sync.Once
	h    interfaces.Hydra
)

// Extend parses extension information.
type Extend struct {
	AccountType string `json:"account_type"`
	ClientType  string `json:"client_type"`
	LoginIP     string `json:"login_ip"`
	UdID        string `json:"udid"`
	VisitorType string `json:"visitor_type"`
	PhoneNumber string `json:"phone_number"`
	VisitorName string `json:"visitor_name"`
}

// IntrospectInfo contains introspection information.
type IntrospectInfo struct {
	Active    bool   `json:"active"`
	Scope     string `json:"scope"`
	ClientID  string `json:"client_id"`
	SubID     string `json:"sub"`
	TokenType string `json:"token_type"`
	Ext       Extend `json:"ext"`
}

const introspectURI = "/oauth2/introspect"

type noopHydra struct{}

func (n *noopHydra) Introspect(_ context.Context, _ string) (*interfaces.TokenInfo, error) {
	return &interfaces.TokenInfo{
		Active:     true,
		VisitorTyp: interfaces.Anonymous,
	}, nil
}

// NewHydra creates an authorization service instance.
// When AUTH_ENABLED=false, returns a noop implementation that skips token verification.
func NewHydra() interfaces.Hydra {
	once.Do(func() {
		conf := config.NewConfigLoader()
		if !config.GetAuthEnabled() {
			conf.GetLogger().Warn("ISF authentication disabled via AUTH_ENABLED env, using noop hydra")
			h = &noopHydra{}
		} else {
			h = &hydra{
				adminAddress: conf.OAuth.BuildAdminURL(),
				logger:       conf.GetLogger(),
				httpClient:   rest.NewHTTPClient(),
			}
		}
	})
	return h
}

// Introspect introspects a token.
func (h *hydra) Introspect(ctx context.Context, token string) (info *interfaces.TokenInfo, err error) {
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
	// Scope permission range.
	info.Scope = introspectInfo.Scope
	// Client ID.
	info.ClientID = introspectInfo.ClientID
	// Client credentials mode.
	if info.VisitorID == info.ClientID {
		info.VisitorTyp = interfaces.Business
		return
	}
	// The following fields exist only outside client-credentials mode.
	// Visitor type.
	info.VisitorTyp = interfaces.VisitorType(introspectInfo.Ext.VisitorType)

	// Anonymous user.
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
	return
}
