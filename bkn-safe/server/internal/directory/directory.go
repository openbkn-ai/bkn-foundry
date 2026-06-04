// Package directory is bkn-safe's user/organization directory: users,
// departments, groups, and name resolution. This is a CLEAN redesign of the
// ISF user-management surface (no GET-in-body, no array-vs-map quirks) — the
// consuming services are migrated to call it.
package directory

import (
	"context"

	"gorm.io/gorm"

	"bkn-safe/internal/model"
)

// Service provides directory queries over GORM.
type Service struct {
	db *gorm.DB
}

// New builds the directory service.
func New(db *gorm.DB) *Service { return &Service{db: db} }

// NamedRef is a resolved { id, name } pair.
type NamedRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// UserDetail is the full user view (with roles + department chain ids).
type UserDetail struct {
	ID          string   `json:"id"`
	Account     string   `json:"account"`
	Name        string   `json:"name"`
	Email       string   `json:"email"`
	Telephone   string   `json:"telephone"`
	Enabled     bool     `json:"enabled"`
	AccountType string   `json:"account_type"`
	Roles       []string `json:"roles"`
	Departments []string `json:"departments"`
}

// GetUser returns a user's detail, or gorm.ErrRecordNotFound.
func (s *Service) GetUser(ctx context.Context, id string) (*UserDetail, error) {
	var u model.User
	if err := s.db.WithContext(ctx).First(&u, "id = ?", id).Error; err != nil {
		return nil, err
	}
	d := &UserDetail{
		ID: u.ID, Account: u.Account, Name: u.Name, Email: u.Email,
		Telephone: u.Telephone, Enabled: u.Enabled, AccountType: string(u.AccountType),
	}
	// department ids
	var uds []model.UserDepartment
	s.db.WithContext(ctx).Where("user_id = ?", id).Find(&uds)
	for _, ud := range uds {
		d.Departments = append(d.Departments, ud.DepartmentID)
	}
	return d, nil
}

// ResolveUserNames maps user ids to {id,name}. Unknown ids are omitted (the
// clean contract returns what it finds; callers handle gaps).
func (s *Service) ResolveUserNames(ctx context.Context, ids []string) ([]NamedRef, error) {
	return s.resolveNames(ctx, &model.User{}, ids)
}

// ResolveDepartmentNames maps department ids to {id,name}.
func (s *Service) ResolveDepartmentNames(ctx context.Context, ids []string) ([]NamedRef, error) {
	return s.resolveNames(ctx, &model.Department{}, ids)
}

// ResolveGroupNames maps group ids to {id,name}.
func (s *Service) ResolveGroupNames(ctx context.Context, ids []string) ([]NamedRef, error) {
	return s.resolveNames(ctx, &model.Group{}, ids)
}

// ResolveAppNames maps application-account ids to {id,name}. App accounts are
// User rows (account_type=app), so resolution is a plain users-table lookup.
func (s *Service) ResolveAppNames(ctx context.Context, ids []string) ([]NamedRef, error) {
	return s.resolveNames(ctx, &model.User{}, ids)
}

// ResolveContactorNames maps contactor ids to {id,name} (User rows,
// account_type=contactor).
func (s *Service) ResolveContactorNames(ctx context.Context, ids []string) ([]NamedRef, error) {
	return s.resolveNames(ctx, &model.User{}, ids)
}

// resolveNames is the shared id->name lookup for any model with id+name columns.
func (s *Service) resolveNames(ctx context.Context, m any, ids []string) ([]NamedRef, error) {
	out := make([]NamedRef, 0, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	if err := s.db.WithContext(ctx).Model(m).
		Select("id", "name").Where("id IN ?", ids).Scan(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// ListDepartments returns departments under a parent ("" = roots).
func (s *Service) ListDepartments(ctx context.Context, parentID string) ([]model.Department, error) {
	var deps []model.Department
	if err := s.db.WithContext(ctx).Where("parent_id = ?", parentID).Find(&deps).Error; err != nil {
		return nil, err
	}
	return deps, nil
}

// GroupMembers returns the member user ids of a group.
func (s *Service) GroupMembers(ctx context.Context, groupID string) ([]string, error) {
	var ms []model.GroupMember
	if err := s.db.WithContext(ctx).Where("group_id = ?", groupID).Find(&ms).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(ms))
	for _, m := range ms {
		ids = append(ids, m.MemberID)
	}
	return ids, nil
}

// SearchOrg returns, from the given user ids, those that belong to any
// department in scope (membership check against the org subtree). Mirrors the
// ISF search-org intent with a clean signature.
func (s *Service) SearchOrg(ctx context.Context, userIDs, scopeDeptIDs []string) ([]string, error) {
	if len(userIDs) == 0 || len(scopeDeptIDs) == 0 {
		return []string{}, nil
	}
	var uds []model.UserDepartment
	if err := s.db.WithContext(ctx).
		Where("user_id IN ? AND department_id IN ?", userIDs, scopeDeptIDs).
		Find(&uds).Error; err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(uds))
	for _, ud := range uds {
		if !seen[ud.UserID] {
			seen[ud.UserID] = true
			out = append(out, ud.UserID)
		}
	}
	return out, nil
}
