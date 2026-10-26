// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// Merge conflict reasons.
const (
	// MERGE_CONFLICT_BOTH_CHANGED: both sides changed the same field, or the same opaque file, differently.
	MERGE_CONFLICT_BOTH_CHANGED = "both_changed"
	// MERGE_CONFLICT_DELETED_BY_OURS: ours deleted a definition that theirs changed.
	MERGE_CONFLICT_DELETED_BY_OURS = "deleted_by_ours"
	// MERGE_CONFLICT_DELETED_BY_THEIRS: theirs deleted a definition that ours changed.
	MERGE_CONFLICT_DELETED_BY_THEIRS = "deleted_by_theirs"
)

// MERGE_TYPE_FILE is the conflict type of a file that is not a BKN definition, such as
// object_metrics/*.json or SKILL.md. Such files are merged whole, by path.
const MERGE_TYPE_FILE = "file"

// MergeConflict is one change that the three-way merge could not reconcile.
type MergeConflict struct {
	// Type is the definition kind (network, object_type, relation_type, ...) or MERGE_TYPE_FILE.
	Type string `json:"type"`
	// ID is the definition id, or the path of a file that is not a definition.
	ID string `json:"id"`
	// File is the path of the definition inside bkn/.
	File   string `json:"file"`
	Reason string `json:"reason"`
	// Path names the field the way the BKN SDK names a field change, for example
	// "data_properties[alt_part].description". It is empty when the whole definition conflicts.
	Path string `json:"path,omitempty"`
	// Base, Ours and Theirs are the field values on each side; empty when absent.
	Base   string `json:"base,omitempty"`
	Ours   string `json:"ours,omitempty"`
	Theirs string `json:"theirs,omitempty"`
}

// MergeSummary counts the definitions and files of a merge by outcome.
type MergeSummary struct {
	// Unchanged: neither side changed it.
	Unchanged int `json:"unchanged"`
	// Ours: only ours changed or added it, or both made the same change.
	Ours int `json:"ours"`
	// Theirs: only theirs changed or added it.
	Theirs int `json:"theirs"`
	// Merged: both changed it and the changes were combined field by field without conflict.
	Merged int `json:"merged"`
	// Deleted: one side deleted it and the other left it unchanged, or both deleted it.
	Deleted int `json:"deleted"`
	// Conflicted: at least one conflict was recorded for it.
	Conflicted int `json:"conflicted"`
}

// MergeResult is the outcome of a three-way merge of two bkn/ file sets onto their merge base.
type MergeResult struct {
	// Files is the merged bkn/ file set. A conflicting field, file or definition keeps the ours
	// side, so Files is always a complete set; the caller decides what to do with Conflicts.
	Files     map[string][]byte `json:"-"`
	Conflicts []MergeConflict   `json:"conflicts"`
	Summary   MergeSummary      `json:"summary"`
}

// HasConflicts reports whether the merge needs a decision from a person.
func (r *MergeResult) HasConflicts() bool {
	return len(r.Conflicts) > 0
}
