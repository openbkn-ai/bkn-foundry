// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

var (
	haAccessOnce sync.Once
	haAccess     interfaces.AuthAccess
)

type hydraAuthAccess struct {
	appSetting *common.AppSetting
	hydra      hydra.Hydra
	// adminURL is Hydra's admin base, where TokenExpiry introspects.
	adminURL string
	client   *http.Client
}

func NewHydraAuthAccess(appSetting *common.AppSetting) interfaces.AuthAccess {
	haAccessOnce.Do(func() {
		haAccess = &hydraAuthAccess{
			appSetting: appSetting,
			hydra:      hydra.NewHydra(appSetting.HydraAdminSetting),
			adminURL: fmt.Sprintf("http://%s:%d", appSetting.HydraAdminSetting.HydraAdminHost,
				appSetting.HydraAdminSetting.HydraAdminPort),
			client: &http.Client{Timeout: 10 * time.Second},
		}
	})

	return haAccess
}

func (haa *hydraAuthAccess) VerifyToken(ctx context.Context, c *gin.Context) (hydra.Visitor, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "VerifyToken")
	defer span.End()

	visitor, err := haa.hydra.VerifyToken(ctx, c)
	if err != nil {
		span.SetStatus(codes.Error, "Verify token failed")
		return visitor, err
	}

	span.SetStatus(codes.Ok, "")
	return visitor, nil
}

// TokenExpiry introspects token at Hydra's admin API and reads its exp. The token is sent in the
// form body only, never logged.
func (haa *hydraAuthAccess) TokenExpiry(ctx context.Context, token string) (time.Time, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "TokenExpiry")
	defer span.End()

	token = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(token), "Bearer "))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, haa.adminURL+"/admin/oauth2/introspect",
		strings.NewReader(url.Values{"token": {token}}.Encode()))
	if err != nil {
		span.SetStatus(codes.Error, "Build request failed")
		return time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := haa.client.Do(req)
	if err != nil {
		span.SetStatus(codes.Error, "Introspect failed")
		return time.Time{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		span.SetStatus(codes.Error, "Introspect failed")
		return time.Time{}, fmt.Errorf("introspect: status %d", resp.StatusCode)
	}
	var info struct {
		Active bool  `json:"active"`
		Exp    int64 `json:"exp"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info); err != nil {
		span.SetStatus(codes.Error, "Decode failed")
		return time.Time{}, err
	}
	if !info.Active || info.Exp <= 0 {
		span.SetStatus(codes.Error, "Token not active")
		return time.Time{}, errors.New("token is not active")
	}
	span.SetStatus(codes.Ok, "")
	return time.Unix(info.Exp, 0), nil
}
