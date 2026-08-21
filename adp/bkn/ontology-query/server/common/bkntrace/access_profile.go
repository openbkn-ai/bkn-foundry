// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package bkntrace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/bytedance/sonic"
)

const (
	maxBknSafeResponseBytes = 4 << 20
	defaultBknSafeBaseURL   = "http://bkn-safe:3000"
)

var ErrAccessProfileDenied = errors.New("BKN Safe access profile is denied")

type AccessProfile struct {
	ActorID string
	Roles   []string
}

// ResolveAccessProfile follows the same BKN Safe contract as BKN Trace Core:
// verify the active caller through /me, then resolve permissions before using
// the built-in role set for an administrative decision.
func ResolveAccessProfile(ctx context.Context, authorization, expectedActorID string) (AccessProfile, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("BKN_SAFE_BASE_URL")), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(strings.TrimSpace(os.Getenv("BKN_SAFE_URL")), "/")
	}
	if baseURL == "" {
		baseURL = defaultBknSafeBaseURL
	}
	if strings.TrimSpace(authorization) == "" || strings.TrimSpace(expectedActorID) == "" {
		return AccessProfile{}, fmt.Errorf("%w: trusted authorization identity is incomplete", ErrAccessProfileDenied)
	}
	client := &http.Client{Timeout: accessProfileTimeout()}
	var me struct {
		ID      string   `json:"id"`
		Enabled bool     `json:"enabled"`
		Roles   []string `json:"roles"`
	}
	if err := getBknSafe(ctx, client, baseURL+"/api/safe/v1/me", authorization, &me); err != nil {
		return AccessProfile{}, fmt.Errorf("resolve current BKN Safe identity: %w", err)
	}
	if !me.Enabled || strings.TrimSpace(me.ID) == "" || me.ID != expectedActorID {
		return AccessProfile{}, fmt.Errorf("%w: current BKN Safe identity is disabled or does not match the trusted actor", ErrAccessProfileDenied)
	}
	var permissions struct {
		Permissions []json.RawMessage `json:"permissions"`
	}
	if err := getBknSafe(ctx, client, baseURL+"/api/safe/v1/me/permissions", authorization, &permissions); err != nil {
		return AccessProfile{}, fmt.Errorf("resolve current BKN Safe permissions: %w", err)
	}
	return AccessProfile{ActorID: me.ID, Roles: builtInRoles(me.Roles)}, nil
}

func (p AccessProfile) IsOutboxAdmin() bool {
	for _, role := range p.Roles {
		if role == "admin" || role == "super_admin" {
			return true
		}
	}
	return false
}

func getBknSafe(ctx context.Context, client *http.Client, url, authorization string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", authorization)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxBknSafeResponseBytes))
		return fmt.Errorf("BKN Safe returned status %d", response.StatusCode)
	}
	return sonic.ConfigDefault.NewDecoder(io.LimitReader(response.Body, maxBknSafeResponseBytes)).Decode(target)
}

func builtInRoles(values []string) []string {
	allowed := map[string]struct{}{"super_admin": {}, "admin": {}, "security": {}, "audit": {}, "network_builder": {}, "normal_user": {}}
	roles := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		role := strings.TrimSpace(value)
		if _, ok := allowed[role]; !ok {
			continue
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles
}

func accessProfileTimeout() time.Duration {
	if value, err := time.ParseDuration(strings.TrimSpace(os.Getenv("BKN_SAFE_ACCESS_TIMEOUT"))); err == nil && value > 0 {
		return value
	}
	return 3 * time.Second
}
