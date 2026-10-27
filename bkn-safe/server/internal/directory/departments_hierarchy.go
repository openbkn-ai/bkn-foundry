// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package directory

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// ErrDepartmentCycle also covers attaching a node to an already cyclic chain.
var ErrDepartmentCycle = errors.New("department parent would create or join a cycle")

// lockDepartmentHierarchy reads the current hierarchy under database locks held
// until commit. Locking the entire forest in ID order serializes reparenting
// across replicas, including moves between roots. Locking only the moving node
// is insufficient: simultaneous A->B and B->A could both pass validation.
// Department writes are infrequent; this deliberately favors correctness over
// fine-grained locking without introducing a lock table or schema migration.
func lockDepartmentHierarchy(tx *gorm.DB) (map[string]string, error) {
	var departments []model.Department
	if err := tx.Select("id", "parent_id").Order("id").
		Clauses(clause.Locking{Strength: "UPDATE"}).Find(&departments).Error; err != nil {
		return nil, err
	}
	parents := make(map[string]string, len(departments))
	for _, dept := range departments {
		parents[dept.ID] = dept.ParentID
	}
	return parents, nil
}

func validateDepartmentParent(parents map[string]string, id, parentID string) error {
	seen := map[string]bool{id: true}
	for current := parentID; current != ""; {
		if seen[current] {
			return ErrDepartmentCycle
		}
		seen[current] = true
		parent, ok := parents[current]
		if !ok {
			return fmt.Errorf("%w: %s", ErrUnknownDepartment, current)
		}
		current = parent
	}
	return nil
}
