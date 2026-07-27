// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package directory

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// ErrDuplicateDepartmentCode is returned when a non-empty department code is
// already assigned to another department.
var ErrDuplicateDepartmentCode = errors.New("department code already exists")

// DepartmentWriteInput is the normalized mutable department payload validated
// before create/update.
type DepartmentWriteInput struct {
	Name      string
	ParentID  string
	Type      string
	ManagerID string
	Code      string
	Email     string
	Remark    string
}

// DepartmentDetail is the admin API view of a single department (snake_case JSON).
type DepartmentDetail struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ParentID    string `json:"parent_id"`
	Type        string `json:"type"`
	ManagerID   string `json:"manager_id,omitempty"`
	ManagerName string `json:"manager_name,omitempty"`
	Code        string `json:"code,omitempty"`
	Email       string `json:"email,omitempty"`
	Remark      string `json:"remark,omitempty"`
	CreatedAt   any    `json:"created_at,omitempty"`
}

func normalizeDepartmentWrite(in DepartmentWriteInput) DepartmentWriteInput {
	in.Name = strings.TrimSpace(in.Name)
	in.ParentID = strings.TrimSpace(in.ParentID)
	in.Type = strings.TrimSpace(in.Type)
	in.ManagerID = strings.TrimSpace(in.ManagerID)
	in.Code = strings.TrimSpace(in.Code)
	in.Email = strings.TrimSpace(in.Email)
	in.Remark = strings.TrimSpace(in.Remark)
	return in
}

// ValidateDepartmentWrite checks manager existence, code uniqueness, and email shape.
// deptID is the department being updated ("" on create).
func (s *Service) ValidateDepartmentWrite(ctx context.Context, in DepartmentWriteInput, deptID string) error {
	in = normalizeDepartmentWrite(in)
	if in.ManagerID != "" {
		if err := s.requireUsersExist(ctx, []string{in.ManagerID}); err != nil {
			return err
		}
	}
	if in.Email != "" {
		if _, err := mail.ParseAddress(in.Email); err != nil {
			return fmt.Errorf("invalid department email: %w", err)
		}
	}
	if in.Code == "" {
		return nil
	}
	var count int64
	q := s.db.WithContext(ctx).Model(&model.Department{}).Where("code = ?", in.Code)
	if deptID != "" {
		q = q.Where("id <> ?", deptID)
	}
	if err := q.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrDuplicateDepartmentCode
	}
	return nil
}

func departmentDetailFrom(d model.Department, managerName string) DepartmentDetail {
	return DepartmentDetail{
		ID:          d.ID,
		Name:        d.Name,
		ParentID:    d.ParentID,
		Type:        d.Type,
		ManagerID:   d.ManagerID,
		ManagerName: managerName,
		Code:        d.Code,
		Email:       d.Email,
		Remark:      d.Remark,
		CreatedAt:   d.CreatedAt,
	}
}

// GetDepartmentDetail returns a department with resolved manager_name.
func (s *Service) GetDepartmentDetail(ctx context.Context, id string) (*DepartmentDetail, error) {
	d, err := s.GetDepartment(ctx, id)
	if err != nil {
		return nil, err
	}
	managerName := ""
	if d.ManagerID != "" {
		names, err := s.ResolveUserNames(ctx, []string{d.ManagerID})
		if err != nil {
			return nil, err
		}
		if len(names) > 0 {
			managerName = names[0].Name
		}
	}
	out := departmentDetailFrom(*d, managerName)
	return &out, nil
}

func (s *Service) managerNamesByID(ctx context.Context, managerIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(managerIDs) == 0 {
		return out, nil
	}
	seen := map[string]struct{}{}
	unique := make([]string, 0, len(managerIDs))
	for _, id := range managerIDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return out, nil
	}
	names, err := s.ResolveUserNames(ctx, unique)
	if err != nil {
		return nil, err
	}
	for _, ref := range names {
		out[ref.ID] = ref.Name
	}
	return out, nil
}

// ApplyDepartmentWrite maps validated input onto a department model.
func ApplyDepartmentWrite(d *model.Department, in DepartmentWriteInput) {
	in = normalizeDepartmentWrite(in)
	d.Name = in.Name
	d.ParentID = in.ParentID
	d.Type = in.Type
	d.ManagerID = in.ManagerID
	d.Code = in.Code
	d.Email = in.Email
	d.Remark = in.Remark
}

// DepartmentPatchRequest is the partial update body for PUT /admin/departments/:id.
type DepartmentPatchRequest struct {
	Name      *string `json:"name"`
	ParentID  *string `json:"parent_id"`
	Type      *string `json:"type"`
	ManagerID *string `json:"manager_id"`
	Code      *string `json:"code"`
	Email     *string `json:"email"`
	Remark    *string `json:"remark"`
}

// PatchDepartmentFields builds a GORM updates map from optional pointers.
// Present pointers apply the value (including empty string to clear).
func PatchDepartmentFields(req DepartmentPatchRequest) map[string]any {
	fields := map[string]any{}
	if req.Name != nil {
		fields["name"] = strings.TrimSpace(*req.Name)
	}
	if req.ParentID != nil {
		fields["parent_id"] = strings.TrimSpace(*req.ParentID)
	}
	if req.Type != nil {
		fields["type"] = strings.TrimSpace(*req.Type)
	}
	if req.ManagerID != nil {
		fields["manager_id"] = strings.TrimSpace(*req.ManagerID)
	}
	if req.Code != nil {
		fields["code"] = strings.TrimSpace(*req.Code)
	}
	if req.Email != nil {
		fields["email"] = strings.TrimSpace(*req.Email)
	}
	if req.Remark != nil {
		fields["remark"] = strings.TrimSpace(*req.Remark)
	}
	return fields
}

// ValidateDepartmentPatch validates a partial update against the merged result.
func (s *Service) ValidateDepartmentPatch(ctx context.Context, deptID string, current model.Department, fields map[string]any) error {
	merged := current
	if v, ok := fields["name"].(string); ok {
		merged.Name = v
	}
	if v, ok := fields["parent_id"].(string); ok {
		merged.ParentID = v
	}
	if v, ok := fields["type"].(string); ok {
		merged.Type = v
	}
	if v, ok := fields["manager_id"].(string); ok {
		merged.ManagerID = v
	}
	if v, ok := fields["code"].(string); ok {
		merged.Code = v
	}
	if v, ok := fields["email"].(string); ok {
		merged.Email = v
	}
	if v, ok := fields["remark"].(string); ok {
		merged.Remark = v
	}
	return s.ValidateDepartmentWrite(ctx, DepartmentWriteInput{
		Name:      merged.Name,
		ParentID:  merged.ParentID,
		Type:      merged.Type,
		ManagerID: merged.ManagerID,
		Code:      merged.Code,
		Email:     merged.Email,
		Remark:    merged.Remark,
	}, deptID)
}
