// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package directory

import (
	"context"
	"sort"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/identity"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DeleteEmptyDepartments deletes only requested empty leaves, recursively.
// Local children and members retain their ancestors. The forest is read and
// locked once, rather than once per deleted node; companion cleanup still
// participates in the same transaction. The result lists retained IDs.
func (s *Service) DeleteEmptyDepartments(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var retained []string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		parents, err := lockDepartmentHierarchy(tx)
		if err != nil {
			return err
		}
		pending := make(map[string]bool, len(ids))
		for _, id := range ids {
			if _, ok := parents[id]; !ok {
				return gorm.ErrRecordNotFound
			}
			pending[id] = true
		}
		children := make(map[string]int)
		for _, parent := range parents {
			children[parent]++
		}
		members := make(map[string]bool)
		for start := 0; start < len(ids); start += 500 {
			end := min(start+500, len(ids))
			var occupied []string
			if err := tx.Model(&model.UserDepartment{}).Where("department_id IN ?", ids[start:end]).Clauses(clause.Locking{Strength: "UPDATE"}).Pluck("department_id", &occupied).Error; err != nil {
				return err
			}
			for _, id := range occupied {
				members[id] = true
			}
		}
		var leaves []string
		for id := range pending {
			if children[id] == 0 && !members[id] {
				leaves = append(leaves, id)
			}
		}
		for len(leaves) > 0 {
			sort.Strings(leaves)
			for _, id := range leaves {
				if err := identity.CleanupDepartment(tx, id); err != nil {
					return err
				}
			}
			for start := 0; start < len(leaves); start += 500 {
				if err := tx.Where("id IN ?", leaves[start:min(start+500, len(leaves))]).Delete(&model.Department{}).Error; err != nil {
					return err
				}
			}
			var next []string
			for _, id := range leaves {
				delete(pending, id)
				parent := parents[id]
				children[parent]--
				if pending[parent] && children[parent] == 0 && !members[parent] {
					next = append(next, parent)
				}
			}
			leaves = next
		}
		for id := range pending {
			retained = append(retained, id)
		}
		sort.Strings(retained)
		return nil
	})
	return retained, err
}
