// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package accountname

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// adminRoles are the bkn-safe roles that administer the platform.
var adminRoles = []string{"admin", "super_admin"}

// NewAdminAccess tells administrators through bkn-safe at BKN_SAFE_URL; without it, no one is.
func NewAdminAccess() interfaces.AdminAccess {
	return NewAdminAccessAt(os.Getenv(BknSafeURLEnv))
}

// NewAdminAccessAt tells administrators through bkn-safe at baseURL.
func NewAdminAccessAt(baseURL string) interfaces.AdminAccess {
	return &accountNameAccess{url: strings.TrimRight(strings.TrimSpace(baseURL), "/"), client: &http.Client{Timeout: lookupTimeout}}
}

// IsAdmin reads the caller's roles from bkn-safe's /me; anything it cannot read is no administrator.
func (a *accountNameAccess) IsAdmin(ctx context.Context, token string) bool {
	if a.url == "" || token == "" {
		return false
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.url+"/api/safe/v1/me", nil)
	if err != nil {
		return false
	}
	request.Header.Set("Authorization", token)
	resp, err := a.client.Do(request)
	if err != nil {
		logger.Warnf("Get caller roles failed: %v", err)
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		logger.Warnf("Get caller roles failed: HTTP %d", resp.StatusCode)
		return false
	}
	var me struct {
		Enabled bool     `json:"enabled"`
		Roles   []string `json:"roles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		logger.Warnf("Get caller roles failed: %v", err)
		return false
	}
	return me.Enabled && slices.ContainsFunc(me.Roles, func(role string) bool { return slices.Contains(adminRoles, role) })
}
