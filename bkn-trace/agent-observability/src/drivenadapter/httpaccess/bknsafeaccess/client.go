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

type meResponse struct {
	ID      string   `json:"id"`
	Enabled bool     `json:"enabled"`
	Roles   []string `json:"roles"`
}

type permissionsResponse struct {
	Permissions []struct {
		Resource struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"resource"`
		Operations []string `json:"operations"`
	} `json:"permissions"`
}

type fingerprintInput struct {
	TenantID                    string
	BusinessDomain              string
	ActorID                     string
	EffectiveSubjectID          string
	ApplicationPrincipalID      string
	DelegationID                string
	Roles                       []string
	ManagedKnowledgeNetworkIDs  []string
	ManagesAllKnowledgeNetworks bool
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
		identity.TenantID == "" || identity.ActorID == "" || identity.EffectiveSubjectID == "" {
		return evidencevo.AccessProfile{}, errors.New("trusted authorization identity is incomplete")
	}

	var me meResponse
	if err := c.get(ctx, "/api/safe/v1/me", authorization, &me); err != nil {
		return evidencevo.AccessProfile{}, fmt.Errorf("resolve current BKN Safe identity: %w", err)
	}
	if !me.Enabled || strings.TrimSpace(me.ID) == "" || me.ID != identity.ActorID {
		return evidencevo.AccessProfile{}, errors.New("current BKN Safe identity is disabled or does not match the trusted actor")
	}

	var permissions permissionsResponse
	if err := c.get(ctx, "/api/safe/v1/me/permissions", authorization, &permissions); err != nil {
		return evidencevo.AccessProfile{}, fmt.Errorf("resolve current BKN Safe permissions: %w", err)
	}

	roles := currentBuiltInRoles(me.Roles)
	managedNetworks := concreteManagedNetworks(permissions)
	managesAllNetworks := contains(roles, "network_builder") && hasTypeWideNetworkManagement(permissions)
	input := fingerprintInput{
		TenantID: identity.TenantID, BusinessDomain: identity.BusinessDomain,
		ActorID: identity.ActorID, EffectiveSubjectID: identity.EffectiveSubjectID,
		ApplicationPrincipalID: identity.ApplicationPrincipalID, DelegationID: identity.DelegationID,
		Roles: roles, ManagedKnowledgeNetworkIDs: managedNetworks,
		ManagesAllKnowledgeNetworks: managesAllNetworks,
	}
	return evidencevo.AccessProfile{
		TenantID: identity.TenantID, BusinessDomain: identity.BusinessDomain,
		ActorID: identity.ActorID, EffectiveSubjectID: identity.EffectiveSubjectID,
		ApplicationPrincipalID: identity.ApplicationPrincipalID, DelegationID: identity.DelegationID,
		Roles: roles, ManagedKnowledgeNetworkIDs: managedNetworks,
		ManagesAllKnowledgeNetworks: managesAllNetworks,
		AccountActive:               true, TenantActive: true,
		Fingerprint: accessScopeFingerprint(input),
	}, nil
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
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxSafeResponseBytes))
		return fmt.Errorf("BKN Safe returned status %d", response.StatusCode)
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

func concreteManagedNetworks(response permissionsResponse) []string {
	networks := map[string]struct{}{}
	for _, permission := range response.Permissions {
		if permission.Resource.Type != "knowledge_network" || permission.Resource.ID == "" || permission.Resource.ID == "*" {
			continue
		}
		for _, operation := range permission.Operations {
			if _, allowed := networkManagementOperations[operation]; allowed {
				networks[permission.Resource.ID] = struct{}{}
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

func hasTypeWideNetworkManagement(response permissionsResponse) bool {
	for _, permission := range response.Permissions {
		if permission.Resource.Type != "knowledge_network" || permission.Resource.ID != "*" {
			continue
		}
		for _, operation := range permission.Operations {
			if _, allowed := networkManagementOperations[operation]; allowed {
				return true
			}
		}
	}
	return false
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func accessScopeFingerprint(input fingerprintInput) string {
	roles := append([]string(nil), input.Roles...)
	networks := append([]string(nil), input.ManagedKnowledgeNetworkIDs...)
	sort.Strings(roles)
	sort.Strings(networks)
	body, _ := json.Marshal(struct {
		TenantID                    string   `json:"tenant_id"`
		BusinessDomain              string   `json:"business_domain"`
		ActorID                     string   `json:"actor_id"`
		EffectiveSubjectID          string   `json:"effective_subject_id"`
		ApplicationPrincipalID      string   `json:"application_principal_id"`
		DelegationID                string   `json:"delegation_id"`
		Roles                       []string `json:"roles"`
		ManagedKnowledgeNetworkIDs  []string `json:"managed_knowledge_network_ids"`
		ManagesAllKnowledgeNetworks bool     `json:"manages_all_knowledge_networks"`
	}{
		input.TenantID, input.BusinessDomain, input.ActorID, input.EffectiveSubjectID,
		input.ApplicationPrincipalID, input.DelegationID, roles, networks,
		input.ManagesAllKnowledgeNetworks,
	})
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
