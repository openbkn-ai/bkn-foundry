// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package sso

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

type externalDepartment struct {
	Key, Name, ParentKey string
	Member               bool
	SyncMetadata         bool
}

type casdoorDepartmentObject struct {
	Owner       string `json:"owner"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	ParentID    string `json:"parentId"`
}

func (s *Service) resolveDepartments(ctx context.Context, p *model.SSOProvider, names []string) ([]externalDepartment, error) {
	if p.Adapter != "casdoor" || p.DepartmentClaim != "groups" {
		var result []externalDepartment
		seen := map[string]bool{}
		for _, name := range names {
			if !seen[name] {
				result = append(result, externalDepartment{Key: name, Name: name, Member: true})
				seen[name] = true
			}
		}
		return result, nil
	}
	// Bound the entire ancestor traversal, not just each individual HTTP call.
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	resolved := map[string]int{}
	visiting := map[string]bool{}
	var result []externalDepartment
	var visit func(string, int) error
	visit = func(key string, depth int) error {
		if _, ok := resolved[key]; ok {
			return nil
		}
		if visiting[key] || depth > 32 || len(visiting) >= 128 || len(key) > 255 || strings.TrimSpace(key) == "" {
			return fmt.Errorf("invalid Casdoor department hierarchy")
		}
		visiting[key] = true
		owner, name, group := strings.Cut(key, "/")
		if owner == "" || (group && name == "") {
			return ErrInvalidToken
		}
		endpoint, id := "/api/get-group", key
		if !group {
			endpoint, id = "/api/get-organization", "admin/"+key
		}
		obj, err := s.casdoorDepartmentObject(ctx, p, endpoint, id)
		if err != nil {
			return err
		}
		d := externalDepartment{Key: key, Name: obj.DisplayName, SyncMetadata: true}
		if d.Name == "" {
			d.Name = obj.Name
		}
		if strings.TrimSpace(d.Name) == "" || len(d.Name) > 255 {
			return ErrInvalidToken
		}
		if group {
			if obj.Owner != owner || obj.Name != name {
				return ErrInvalidToken
			}
			d.ParentKey = owner
			if obj.ParentID != "" && obj.ParentID != owner {
				d.ParentKey = obj.ParentID
				if !strings.Contains(d.ParentKey, "/") {
					d.ParentKey = owner + "/" + d.ParentKey
				}
				if !strings.HasPrefix(d.ParentKey, owner+"/") {
					return ErrInvalidToken
				}
			}
			if err := visit(d.ParentKey, depth+1); err != nil {
				return err
			}
		} else if obj.Owner != "admin" || obj.Name != key {
			return ErrInvalidToken
		}
		resolved[key] = len(result)
		result = append(result, d) // parents precede children
		return nil
	}
	for _, name := range names {
		if !strings.Contains(name, "/") {
			return nil, ErrInvalidToken
		}
		if err := visit(name, 0); err != nil {
			return nil, err
		}
		result[resolved[name]].Member = true
	}
	return result, nil
}

func (s *Service) casdoorDepartmentObject(ctx context.Context, p *model.SSOProvider, endpoint, id string) (*casdoorDepartmentObject, error) {
	secret := providerSecret(p)
	if p.ClientID == "" || secret == "" {
		return nil, ErrInvalidProvider
	}
	uri := strings.TrimRight(p.Issuer, "/") + endpoint + "?" + url.Values{"id": {id}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(p.ClientID, secret)
	// Do not forward application credentials to a redirected API endpoint.
	client := *s.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Casdoor department API request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Casdoor department API status %d", resp.StatusCode)
	}
	var response struct {
		Status string                   `json:"status"`
		Data   *casdoorDepartmentObject `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&response); err != nil {
		return nil, fmt.Errorf("invalid Casdoor department API response")
	}
	if response.Status != "ok" || response.Data == nil {
		return nil, fmt.Errorf("Casdoor department metadata unavailable")
	}
	return response.Data, nil
}
