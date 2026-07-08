// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import "io/fs"

// DiffAction describes what should happen to a definition during import.
type DiffAction string

const (
	DiffCreate DiffAction = "create"
	DiffUpdate DiffAction = "update"
	DiffSkip   DiffAction = "skip"
	DiffDelete DiffAction = "delete"
)

// DiffEntry represents a single definition's diff result.
type DiffEntry struct {
	Type        string // "object_type", "relation_type", "action_type", "risk_type"
	ID          string
	Action      DiffAction
	OldChecksum string // empty for create
	NewChecksum string // empty for delete
}

// DiffResult holds the complete diff between two network states.
type DiffResult struct {
	Entries []DiffEntry
}

// Creates returns entries that need to be created.
func (r *DiffResult) Creates() []DiffEntry {
	return r.filterByAction(DiffCreate)
}

// Updates returns entries that need to be updated.
func (r *DiffResult) Updates() []DiffEntry {
	return r.filterByAction(DiffUpdate)
}

// Skips returns entries that are unchanged.
func (r *DiffResult) Skips() []DiffEntry {
	return r.filterByAction(DiffSkip)
}

// Deletes returns entries that should be deleted.
func (r *DiffResult) Deletes() []DiffEntry {
	return r.filterByAction(DiffDelete)
}

// HasChanges returns true if there are creates, updates, or deletes.
func (r *DiffResult) HasChanges() bool {
	for _, e := range r.Entries {
		if e.Action != DiffSkip {
			return true
		}
	}
	return false
}

func (r *DiffResult) filterByAction(action DiffAction) []DiffEntry {
	var out []DiffEntry
	for _, e := range r.Entries {
		if e.Action == action {
			out = append(out, e)
		}
	}
	return out
}

// DiffNetworks compares two networks and produces a diff based on definition checksums.
// oldChecksums and newChecksums map "type:id" -> "sha256:hash".
// Use ComputeNetworkChecksums to generate these maps.
func DiffNetworks(oldChecksums, newChecksums map[string]string) *DiffResult {
	result := &DiffResult{}

	// Check new definitions against old
	for key, newHash := range newChecksums {
		defType, defID := splitChecksumKey(key)
		oldHash, exists := oldChecksums[key]
		if !exists {
			result.Entries = append(result.Entries, DiffEntry{
				Type:        defType,
				ID:          defID,
				Action:      DiffCreate,
				NewChecksum: newHash,
			})
		} else if oldHash != newHash {
			result.Entries = append(result.Entries, DiffEntry{
				Type:        defType,
				ID:          defID,
				Action:      DiffUpdate,
				OldChecksum: oldHash,
				NewChecksum: newHash,
			})
		} else {
			result.Entries = append(result.Entries, DiffEntry{
				Type:        defType,
				ID:          defID,
				Action:      DiffSkip,
				OldChecksum: oldHash,
				NewChecksum: newHash,
			})
		}
	}

	// Check for deletions (in old but not in new)
	for key, oldHash := range oldChecksums {
		if _, exists := newChecksums[key]; !exists {
			defType, defID := splitChecksumKey(key)
			result.Entries = append(result.Entries, DiffEntry{
				Type:        defType,
				ID:          defID,
				Action:      DiffDelete,
				OldChecksum: oldHash,
			})
		}
	}

	return result
}

// ComputeNetworkChecksums computes checksums for all definitions in a network directory.
// Returns a map of "type:id" -> "sha256:hash".
func ComputeNetworkChecksums(fsys FileSystem, root string) (map[string]string, error) {
	abs := fsys.Abs(root)
	checksums := make(map[string]string)

	err := fsys.Walk(abs, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		ext := fsys.Ext(path)
		if ext != ".bkn" {
			return nil
		}
		lines := computeBknChecksumWithFS(fsys, path)
		for _, line := range lines {
			parts := splitChecksumLine(line)
			if parts[0] != "" && parts[1] != "" {
				checksums[parts[0]] = parts[1]
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return checksums, nil
}

func splitChecksumKey(key string) (defType, defID string) {
	idx := indexOf(key, ':')
	if idx < 0 {
		return key, ""
	}
	return key[:idx], key[idx+1:]
}

func splitChecksumLine(line string) [2]string {
	parts := splitN(line, "  ", 2)
	if len(parts) == 2 {
		return [2]string{trimSpace(parts[0]), trimSpace(parts[1])}
	}
	return [2]string{}
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func trimSpace(s string) string {
	return s[firstNonSpace(s) : lastNonSpace(s)+1]
}

func firstNonSpace(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return i
		}
	}
	return len(s)
}

func lastNonSpace(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] != ' ' && s[i] != '\t' {
			return i
		}
	}
	return -1
}

func splitN(s, sep string, n int) []string {
	result := make([]string, 0, n)
	for i := 0; i < n-1; i++ {
		idx := indexStr(s, sep)
		if idx < 0 {
			break
		}
		result = append(result, s[:idx])
		s = s[idx+len(sep):]
	}
	result = append(result, s)
	return result
}

func indexStr(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
