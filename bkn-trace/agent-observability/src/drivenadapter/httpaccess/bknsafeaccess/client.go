// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package bknsafeaccess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iauthorizationscope"
)

const maxSafeResponseBytes = 4 << 20

var builtInRoles = map[string]struct{}{
	"super_admin": {}, "admin": {}, "security": {}, "audit": {},
	"network_builder": {}, "normal_user": {},
}

var networkManagementOperations = map[string]struct{}{
	"modify": {}, "authorize": {}, "task_manage": {},
}

type Client struct {
	baseURL string
	http    *http.Client
}

type responseStatusError struct {
	status int
}

func (e responseStatusError) Error() string {
	return fmt.Sprintf("BKN Safe returned status %d", e.status)
}

type meResponse struct {
	ID      string   `json:"id"`
	Enabled bool     `json:"enabled"`
	Roles   []string `json:"roles"`
}

type knowledgeNetworkGrantsResponse struct {
	Grants []struct {
		KnowledgeNetworkID string   `json:"knowledge_network_id"`
		Operations         []string `json:"operations"`
	} `json:"grants"`
}

type fingerprintInput struct {
	ActorID                    string
	EffectiveSubjectID         string
	ApplicationPrincipalID     string
	DelegationID               string
	Roles                      []string
	ManagedKnowledgeNetworkIDs []string
}

func New(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), http: httpClient}
}

func (c *Client) Resolve(
	ctx context.Context,
	authorization string,
	identity iauthorizationscope.TrustedIdentity,
) (evidencevo.AccessProfile, error) {
	if c.baseURL == "" || strings.TrimSpace(authorization) == "" ||
		identity.ActorID == "" || identity.EffectiveSubjectID == "" {
		return evidencevo.AccessProfile{}, fmt.Errorf("%w: trusted authorization identity is incomplete", iauthorizationscope.ErrDenied)
	}

	var me meResponse
	if err := c.get(ctx, "/api/safe/v1/me", authorization, &me); err != nil {
		return evidencevo.AccessProfile{}, classifyResolveError("resolve current BKN Safe identity", err)
	}
	if !me.Enabled || strings.TrimSpace(me.ID) == "" || me.ID != identity.ActorID {
		return evidencevo.AccessProfile{}, fmt.Errorf("%w: current BKN Safe identity is disabled or does not match the trusted actor", iauthorizationscope.ErrDenied)
	}

	var grants knowledgeNetworkGrantsResponse
	if err := c.get(ctx, "/api/safe/v1/me/knowledge-network-grants", authorization, &grants); err != nil {
		var statusErr responseStatusError
		if !errors.As(err, &statusErr) || statusErr.status != http.StatusNotFound {
			return evidencevo.AccessProfile{}, classifyResolveError("resolve current BKN Safe knowledge-network grants", err)
		}
	}

	roles := currentBuiltInRoles(me.Roles)
	managedNetworks := concreteManagedNetworks(grants)
	input := fingerprintInput{
		ActorID:                    identity.ActorID,
		EffectiveSubjectID:         identity.EffectiveSubjectID,
		ApplicationPrincipalID:     identity.ApplicationPrincipalID,
		DelegationID:               identity.DelegationID,
		Roles:                      roles,
		ManagedKnowledgeNetworkIDs: managedNetworks,
	}
	return evidencevo.AccessProfile{
		ActorID:                    identity.ActorID,
		EffectiveSubjectID:         identity.EffectiveSubjectID,
		ApplicationPrincipalID:     identity.ApplicationPrincipalID,
		DelegationID:               identity.DelegationID,
		Roles:                      roles,
		ManagedKnowledgeNetworkIDs: managedNetworks,
		AccountActive:              true,
		Fingerprint:                accessScopeFingerprint(input),
	}, nil
}

func classifyResolveError(operation string, err error) error {
	var statusErr responseStatusError
	if errors.As(err, &statusErr) && (statusErr.status == http.StatusUnauthorized || statusErr.status == http.StatusForbidden) {
		return fmt.Errorf("%w: %s: %v", iauthorizationscope.ErrDenied, operation, err)
	}
	return fmt.Errorf("%w: %s: %v", iauthorizationscope.ErrUnavailable, operation, err)
}

func (c *Client) get(ctx context.Context, path, authorization string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", authorization)
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxSafeResponseBytes))
		return responseStatusError{status: response.StatusCode}
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxSafeResponseBytes))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode BKN Safe response: %w", err)
	}
	return nil
}

func currentBuiltInRoles(values []string) []string {
	roles := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, role := range values {
		role = strings.TrimSpace(role)
		if _, known := builtInRoles[role]; !known {
			continue
		}
		if _, duplicate := seen[role]; duplicate {
			continue
		}
		seen[role] = struct{}{}
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles
}

func concreteManagedNetworks(response knowledgeNetworkGrantsResponse) []string {
	networks := map[string]struct{}{}
	for _, grant := range response.Grants {
		if grant.KnowledgeNetworkID == "" || grant.KnowledgeNetworkID == "*" {
			continue
		}
		for _, operation := range grant.Operations {
			if _, allowed := networkManagementOperations[operation]; allowed {
				networks[grant.KnowledgeNetworkID] = struct{}{}
				break
			}
		}
	}
	values := make([]string, 0, len(networks))
	for networkID := range networks {
		values = append(values, networkID)
	}
	sort.Strings(values)
	return values
}

func accessScopeFingerprint(input fingerprintInput) string {
	roles := append([]string(nil), input.Roles...)
	networks := append([]string(nil), input.ManagedKnowledgeNetworkIDs...)
	sort.Strings(roles)
	sort.Strings(networks)
	body, _ := json.Marshal(struct {
		ActorID                    string   `json:"actor_id"`
		EffectiveSubjectID         string   `json:"effective_subject_id"`
		ApplicationPrincipalID     string   `json:"application_principal_id"`
		DelegationID               string   `json:"delegation_id"`
		Roles                      []string `json:"roles"`
		ManagedKnowledgeNetworkIDs []string `json:"managed_knowledge_network_ids"`
	}{
		input.ActorID,
		input.EffectiveSubjectID,
		input.ApplicationPrincipalID,
		input.DelegationID,
		roles,
		networks,
	})
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
