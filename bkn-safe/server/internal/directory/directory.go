// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package directory is bkn-safe's user/organization directory: users,
// departments, groups, and name resolution. This is a CLEAN redesign of the
// ISF user-management surface (no GET-in-body, no array-vs-map quirks) — the
// consuming services are migrated to call it.
package directory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"bkn-safe/internal/model"
)

// ErrDepartmentNotEmpty is returned by DeleteDepartment when the department
// still has child departments or member users — the caller must move/remove
// them first (no cascade: deleting a non-empty subtree is too blunt to do
// implicitly).
var ErrDepartmentNotEmpty = errors.New("department not empty")

// ErrUnknownUser is returned by AddDepartmentMembers when one or more user ids
// don't reference an existing user. The membership would dangle (no FK), so the
// whole call is rejected and nothing is written.
var ErrUnknownUser = errors.New("unknown user id")

// ErrUnknownDepartment is returned by SetUserDepartments/DepartmentsExist when
// one or more department ids don't reference an existing department.
var ErrUnknownDepartment = errors.New("unknown department id")

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
	ID          string    `json:"id"`
	Account     string    `json:"account"`
	Name        string    `json:"name"`
	Email       string    `json:"email"`
	Telephone   string    `json:"telephone"`
	Enabled     bool      `json:"enabled"`
	AccountType string    `json:"account_type"`
	Roles       []string  `json:"roles"`
	Departments []string  `json:"departments"`
	UpdatedAt   time.Time `json:"updated_at"`
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
		UpdatedAt: u.UpdatedAt,
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

// UserSummary is the list view of a user (no per-user role/department expansion,
// unlike UserDetail) — used for enumeration/search and department-member lists.
type UserSummary struct {
	ID          string `json:"id"`
	Account     string `json:"account"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	Enabled     bool   `json:"enabled"`
	AccountType string `json:"account_type"`
}

// userSummaryCols is the column set selected into UserSummary.
var userSummaryCols = []string{"id", "account", "name", "email", "enabled", "account_type"}

// ListUsers returns a page of users, optionally filtered by a case-insensitive
// substring match on account or name, plus the total matching count. limit<=0
// defaults to 50 and is capped at 500.
func (s *Service) ListUsers(ctx context.Context, search string, offset, limit int) ([]UserSummary, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	q := s.db.WithContext(ctx).Model(&model.User{})
	if search != "" {
		like := "%" + search + "%"
		q = q.Where("account LIKE ? OR name LIKE ?", like, like)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	out := make([]UserSummary, 0, limit)
	if err := q.Select(userSummaryCols).Order("account").Offset(offset).Limit(limit).Scan(&out).Error; err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// FindUserByAccount returns the user with the exact login account, or
// gorm.ErrRecordNotFound when none matches.
func (s *Service) FindUserByAccount(ctx context.Context, account string) (*UserSummary, error) {
	var u UserSummary
	if err := s.db.WithContext(ctx).Model(&model.User{}).
		Select(userSummaryCols).Where("account = ?", account).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// ListAllDepartments returns a page of departments across the whole tree
// (flat), optionally filtered by a case-insensitive name substring, plus the
// total count. Used by the admin org list/tree (client builds the tree).
func (s *Service) ListAllDepartments(ctx context.Context, search string, offset, limit int) ([]model.Department, int64, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	q := s.db.WithContext(ctx).Model(&model.Department{})
	if search != "" {
		q = q.Where("name LIKE ?", "%"+search+"%")
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	deps := make([]model.Department, 0, limit)
	if err := q.Order("parent_id, name").Offset(offset).Limit(limit).Find(&deps).Error; err != nil {
		return nil, 0, err
	}
	return deps, total, nil
}

// GetDepartment returns a single department by id, or gorm.ErrRecordNotFound.
func (s *Service) GetDepartment(ctx context.Context, id string) (*model.Department, error) {
	var d model.Department
	if err := s.db.WithContext(ctx).First(&d, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &d, nil
}

// DepartmentMembers returns the users directly mapped into a department (the
// UserDepartment rows for this dept, resolved to user summaries). Direct
// members only — not transitive descendants.
func (s *Service) DepartmentMembers(ctx context.Context, deptID string) ([]UserSummary, error) {
	var uds []model.UserDepartment
	if err := s.db.WithContext(ctx).Where("department_id = ?", deptID).Find(&uds).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(uds))
	for _, ud := range uds {
		ids = append(ids, ud.UserID)
	}
	out := make([]UserSummary, 0, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	if err := s.db.WithContext(ctx).Model(&model.User{}).
		Select(userSummaryCols).Where("id IN ?", ids).Order("account").Scan(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// AddDepartmentMembers maps the given users into the department. Idempotent: a
// user already in the department is left as-is (no duplicate, no error). The
// department must exist (else gorm.ErrRecordNotFound) and every user id must
// reference an existing user (else ErrUnknownUser) — nothing is written when
// either check fails.
func (s *Service) AddDepartmentMembers(ctx context.Context, deptID string, userIDs []string) error {
	if err := s.requireDepartment(ctx, deptID); err != nil {
		return err
	}
	ids := dedupeNonEmpty(userIDs)
	if len(ids) == 0 {
		return nil
	}
	if err := s.requireUsersExist(ctx, ids); err != nil {
		return err
	}
	rows := make([]model.UserDepartment, 0, len(ids))
	for _, uid := range ids {
		rows = append(rows, model.UserDepartment{UserID: uid, DepartmentID: deptID})
	}
	// Composite PK (user_id, department_id): skip rows already present so the
	// call is idempotent rather than erroring on a duplicate key.
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}

// RemoveDepartmentMembers removes the given users from the department.
// Idempotent: a user not currently in the department is silently ignored. The
// department must exist (else gorm.ErrRecordNotFound).
func (s *Service) RemoveDepartmentMembers(ctx context.Context, deptID string, userIDs []string) error {
	if err := s.requireDepartment(ctx, deptID); err != nil {
		return err
	}
	ids := dedupeNonEmpty(userIDs)
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).
		Where("department_id = ? AND user_id IN ?", deptID, ids).
		Delete(&model.UserDepartment{}).Error
}

// requireDepartment returns gorm.ErrRecordNotFound when no department has the id.
func (s *Service) requireDepartment(ctx context.Context, id string) error {
	var n int64
	if err := s.db.WithContext(ctx).Model(&model.Department{}).Where("id = ?", id).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// requireUsersExist returns ErrUnknownUser (wrapping the missing ids) unless
// every id references an existing user row.
func (s *Service) requireUsersExist(ctx context.Context, ids []string) error {
	var found []string
	if err := s.db.WithContext(ctx).Model(&model.User{}).
		Where("id IN ?", ids).Pluck("id", &found).Error; err != nil {
		return err
	}
	if len(found) == len(ids) {
		return nil
	}
	have := make(map[string]bool, len(found))
	for _, id := range found {
		have[id] = true
	}
	missing := make([]string, 0)
	for _, id := range ids {
		if !have[id] {
			missing = append(missing, id)
		}
	}
	return fmt.Errorf("%w: %v", ErrUnknownUser, missing)
}

// dedupeNonEmpty returns the input with empty strings and duplicates removed,
// preserving first-seen order.
func dedupeNonEmpty(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// SetUserDepartments REPLACES the user's department memberships with exactly the
// given set (the user-centric counterpart of the department member endpoints):
// an empty/absent list clears all of the user's memberships. Idempotent. The
// user must exist (else gorm.ErrRecordNotFound) and every department id must
// reference an existing department (else ErrUnknownDepartment) — the whole
// replace is transactional, so a bad id leaves the prior memberships untouched.
func (s *Service) SetUserDepartments(ctx context.Context, userID string, deptIDs []string) error {
	if err := s.requireUser(ctx, userID); err != nil {
		return err
	}
	ids := dedupeNonEmpty(deptIDs)
	if err := s.DepartmentsExist(ctx, ids); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userID).Delete(&model.UserDepartment{}).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		rows := make([]model.UserDepartment, 0, len(ids))
		for _, did := range ids {
			rows = append(rows, model.UserDepartment{UserID: userID, DepartmentID: did})
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
	})
}

// requireUser returns gorm.ErrRecordNotFound when no user has the id.
func (s *Service) requireUser(ctx context.Context, id string) error {
	var n int64
	if err := s.db.WithContext(ctx).Model(&model.User{}).Where("id = ?", id).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// DepartmentsExist returns ErrUnknownDepartment (wrapping the missing ids) unless
// every id references an existing department. An empty list is a no-op (nil).
func (s *Service) DepartmentsExist(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	var found []string
	if err := s.db.WithContext(ctx).Model(&model.Department{}).
		Where("id IN ?", ids).Pluck("id", &found).Error; err != nil {
		return err
	}
	if len(found) == len(ids) {
		return nil
	}
	have := make(map[string]bool, len(found))
	for _, id := range found {
		have[id] = true
	}
	missing := make([]string, 0)
	for _, id := range ids {
		if !have[id] {
			missing = append(missing, id)
		}
	}
	return fmt.Errorf("%w: %v", ErrUnknownDepartment, missing)
}

// CreateDepartment inserts a new department node. The caller supplies the id
// (or sets it beforehand); ParentID "" makes it a root. Type defaults to
// "department" at the DB layer when empty.
func (s *Service) CreateDepartment(ctx context.Context, d *model.Department) error {
	return s.db.WithContext(ctx).Create(d).Error
}

// UpdateDepartment patches the given mutable fields (name/parent_id/type) of a
// department. Returns gorm.ErrRecordNotFound when no row matches.
func (s *Service) UpdateDepartment(ctx context.Context, id string, fields map[string]any) error {
	res := s.db.WithContext(ctx).Model(&model.Department{}).Where("id = ?", id).Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// DeleteDepartment removes a department, but only if it is empty: no child
// departments and no member users. Returns ErrDepartmentNotEmpty otherwise, or
// gorm.ErrRecordNotFound if the id doesn't exist.
func (s *Service) DeleteDepartment(ctx context.Context, id string) error {
	db := s.db.WithContext(ctx)
	var children int64
	if err := db.Model(&model.Department{}).Where("parent_id = ?", id).Count(&children).Error; err != nil {
		return err
	}
	var members int64
	if err := db.Model(&model.UserDepartment{}).Where("department_id = ?", id).Count(&members).Error; err != nil {
		return err
	}
	if children > 0 || members > 0 {
		return ErrDepartmentNotEmpty
	}
	res := db.Where("id = ?", id).Delete(&model.Department{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
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
