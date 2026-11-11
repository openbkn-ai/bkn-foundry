// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package federation is the assembly boundary for external identity extensions.
// It contains no provider protocol, credentials, companion schema, or token grant.
package federation

import (
	"context"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/identity"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
)

// HydraAdmin exposes core's existing login/session and introspection operations.
// Provider-specific grants and trust registration remain in the extension.
type HydraAdmin = auth.HydraAdmin

func NewHydraAdmin(url string) *HydraAdmin { return auth.NewHydraAdmin(url) }
func NewID() string                        { return auth.NewID() }
func ExtClaims(user *identity.User, ip, clientType string) map[string]any {
	return auth.ExtClaims(user, ip, clientType)
}

var ErrUserDisabled = auth.ErrUserDisabled

type LoginOption struct{ Name, URL string }
type LoginPage struct {
	Options  []LoginOption
	Redirect string
}

type Runtime struct {
	DB                                              *gorm.DB
	Hydra                                           *HydraAdmin
	HydraAdminURL, HydraPublicURL, BrowserPublicURL string
	Redirect                                        func(*gin.Context, string)
}

// Extension is per App, installed between Boot and Run. BeforeLogin can add
// choices or redirect; leaving it nil preserves core's local login. TokenHook
// receives only bodies whose shared hook key core has already authenticated.
type Extension struct {
	BeforeLogin func(*gin.Context) (LoginPage, error)
	Mount       func(*gin.Engine)
	TokenHook   func(context.Context, []byte) (map[string]any, bool, error)
}

type UserStore interface {
	DeleteUser(context.Context, string) error
}
type Directory interface {
	DeleteDepartment(context.Context, string) error
}

func NewUserStore(db *gorm.DB) UserStore { return auth.NewUserStore(db) }
func NewDirectory(db *gorm.DB) Directory { return directory.New(db) }

// DeleteEmptyDepartments preserves core's hierarchy, membership and companion
// cleanup invariants while avoiding one full forest scan per deleted node.
func DeleteEmptyDepartments(ctx context.Context, db *gorm.DB, ids []string) ([]string, error) {
	return directory.New(db).DeleteEmptyDepartments(ctx, ids)
}
