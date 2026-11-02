// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package sso

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type profile struct {
	Subject           string `json:"sub"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	Email             string `json:"email"`
	claims            map[string]json.RawMessage
}

func (s *Service) userInfo(ctx context.Context, p *model.SSOProvider, m *metadata, token, subject string) (*profile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.UserInfo, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OIDC userinfo status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	var u profile
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, err
	}
	if u.Subject == "" || u.Subject != subject {
		return nil, ErrInvalidToken
	}
	if err := json.Unmarshal(body, &u.claims); err != nil {
		return nil, err
	}
	return &u, nil
}

func departmentNames(p *model.SSOProvider, u *profile) ([]string, bool, error) {
	if p.DepartmentClaim == "" {
		return nil, false, nil
	}
	raw, found := u.claims[p.DepartmentClaim]
	if !found {
		return nil, false, nil // an absent claim never erases memberships
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		var one string
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, true, ErrInvalidToken
		}
		if one != "" {
			names = []string{one}
		}
	}
	if len(names) > 64 {
		return nil, true, ErrInvalidToken
	}
	for _, name := range names {
		if strings.TrimSpace(name) == "" || len(name) > 255 {
			return nil, true, ErrInvalidToken
		}
	}
	return names, true, nil
}

// ResolveUser reads the verified UserInfo subject, then creates or updates its
// local identity and only the department memberships owned by this provider.
func (s *Service) ResolveUser(ctx context.Context, p *model.SSOProvider, token, subject string) (*model.User, error) {
	m, err := s.discover(ctx, p)
	if err != nil {
		return nil, err
	}
	profile, err := s.userInfo(ctx, p, m, token, subject)
	if err != nil {
		return nil, err
	}
	departments, hasDepartments, err := departmentNames(p, profile)
	if err != nil {
		return nil, err
	}
	var definitions []externalDepartment
	if hasDepartments {
		definitions, err = s.resolveDepartments(ctx, p, departments)
		if err != nil {
			return nil, err
		}
	}
	var user model.User
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var identity model.SSOIdentity
		err := tx.Where("provider_id = ? AND issuer = ? AND subject = ?", p.ID, p.Issuer, subject).First(&identity).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			hash := sha256.Sum256([]byte(p.ID + "\x00" + p.Issuer + "\x00" + subject))
			account := "sso-" + hex.EncodeToString(hash[:16])
			name := profile.Name
			if name == "" {
				name = profile.PreferredUsername
			}
			if name == "" {
				name = account
			}
			user = model.User{ID: auth.NewID(), Account: account, Name: name, Email: profile.Email,
				Enabled: true, Source: model.SourceSSO, AccountType: model.AccountTypeOther}
			if err := tx.Create(&user).Error; err != nil {
				return err
			}
			identity = model.SSOIdentity{ID: auth.NewID(), ProviderID: p.ID, Issuer: p.Issuer, Subject: subject, UserID: user.ID}
			if err := tx.Create(&identity).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			if err := tx.First(&user, "id = ?", identity.UserID).Error; err != nil {
				return err
			}
			if !user.Enabled || user.Source != model.SourceSSO {
				return auth.ErrUserDisabled
			}
			changes := map[string]any{}
			if profile.Name != "" {
				changes["name"] = profile.Name
			}
			if profile.Email != "" {
				changes["email"] = profile.Email
			}
			if len(changes) > 0 {
				if err := tx.Model(&user).Updates(changes).Error; err != nil {
					return err
				}
			}
		}
		if hasDepartments {
			return syncDepartments(tx, p.ID, user.ID, definitions)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func syncDepartments(tx *gorm.DB, providerID, userID string, definitions []externalDepartment) error {
	for _, d := range definitions {
		if d.SyncMetadata {
			// Use the same forest lock order as directory reparenting, so a
			// simultaneous local edit cannot combine with this batch into a cycle.
			var locked []model.Department
			if err := tx.Select("id").Order("id").Clauses(clause.Locking{Strength: "UPDATE"}).Find(&locked).Error; err != nil {
				return err
			}
			break
		}
	}
	var owned []model.SSODepartment
	if err := tx.Where("provider_id = ?", providerID).Find(&owned).Error; err != nil {
		return err
	}
	ownedIDs := make([]string, 0, len(owned))
	for _, d := range owned {
		ownedIDs = append(ownedIDs, d.DeptID)
	}
	if len(ownedIDs) > 0 {
		if err := tx.Where("user_id = ? AND department_id IN ?", userID, ownedIDs).Delete(&model.UserDepartment{}).Error; err != nil {
			return err
		}
	}
	ids := map[string]string{}
	for _, d := range definitions {
		parentID := ""
		if d.ParentKey != "" {
			var ok bool
			parentID, ok = ids[d.ParentKey]
			if !ok {
				return ErrInvalidToken
			}
		}
		var mapping model.SSODepartment
		err := tx.Where("provider_id = ? AND external_id = ?", providerID, d.Key).First(&mapping).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			dept := model.Department{ID: auth.NewID(), Name: d.Name, ParentID: parentID, Type: "department"}
			if err := tx.Create(&dept).Error; err != nil {
				return err
			}
			mapping = model.SSODepartment{ID: auth.NewID(), ProviderID: providerID, ExternalID: d.Key, DeptID: dept.ID}
			if err := tx.Create(&mapping).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if d.SyncMetadata {
			var dept model.Department
			if err := tx.First(&dept, "id = ?", mapping.DeptID).Error; err != nil {
				return err
			}
			if err := tx.Model(&dept).Updates(map[string]any{"name": d.Name, "parent_id": parentID}).Error; err != nil {
				return err
			}
		}
		ids[d.Key] = mapping.DeptID
		if d.Member {
			if err := tx.Create(&model.UserDepartment{UserID: userID, DepartmentID: mapping.DeptID}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
