// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"
)

// hashHex computes SHA-256 and returns the first 8 bytes as 16 hex chars.
func hashHex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:8])
}

// GenerateChecksumFile validates BKN inputs, then generates CHECKSUM in
// the given business directory. Covers .bkn and SKILL.md. Returns the
// content written.
func GenerateChecksumFile(root string) (string, error) {
	fsys := NewOSFileSystem()
	return GenerateChecksumFileWithFS(fsys, root)
}

// GenerateChecksumFileWithFS generates CHECKSUM using the given FileSystem.
func GenerateChecksumFileWithFS(fsys FileSystem, root string) (string, error) {
	abs := fsys.Abs(root)
	if !fsys.IsDir(abs) {
		return "", fmt.Errorf("not a directory: %s", abs)
	}
	if err := validateChecksumInputsWithFS(fsys, abs); err != nil {
		return "", err
	}

	var entries []string
	err := fsys.Walk(abs, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || fsys.Base(path) == ChecksumFileName {
			return nil
		}
		rel, _ := fsys.Rel(abs, path)
		name := fsys.Base(path)
		ext := fsys.Ext(path)

		if name == "SKILL.md" {
			line := computeSkillChecksumWithFS(fsys, path, rel)
			if line != "" {
				entries = append(entries, line)
			}
		} else if ext == ".bkn" {
			lines := computeBknChecksumWithFS(fsys, path)
			entries = append(entries, lines...)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(entries)

	now := time.Now().Format(time.RFC3339)
	lines := []string{
		"# BKN Directory Checksum",
		"# generated: " + now,
	}
	lines = append(lines, entries...)
	content := strings.Join(lines, "\n") + "\n"

	outPath := fsys.Join(abs, ChecksumFileName)
	if err := fsys.WriteFile(outPath, []byte(content), 0644); err != nil {
		return "", err
	}
	return content, nil
}

func validateChecksumInputsWithFS(fsys FileSystem, root string) error {
	var networkPaths []string
	err := fsys.Walk(root, func(path string, info fs.FileInfo, err error) error {
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

		doc, loadErr := fsys.ReadFile(path)
		if loadErr != nil {
			rel, _ := fsys.Rel(root, path)
			return fmt.Errorf("checksum validation failed for %s: %w", rel, loadErr)
		}
		data, err := ParseFrontmatter(string(doc))
		if err != nil {
			rel, _ := fsys.Rel(root, path)
			return fmt.Errorf("checksum validation failed for %s: %w", rel, err)
		}
		if typeVal, ok := data["type"].(string); ok {
			if strings.EqualFold(strings.TrimSpace(typeVal), "network") {
				networkPaths = append(networkPaths, path)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Check that each directory with a network file has a network.bkn root file
	dirsWithNetworks := make(map[string]bool)
	for _, p := range networkPaths {
		dirsWithNetworks[fsys.Dir(p)] = true
	}
	for d := range dirsWithNetworks {
		rootFile := fsys.Join(d, RootFileName)
		if _, err := fsys.Stat(rootFile); err != nil {
			return fmt.Errorf("checksum validation failed: %s not found in %s", RootFileName, d)
		}
	}
	return nil
}

// VerifyChecksumFile verifies CHECKSUM against actual files.
// Returns (ok, errorMessages).
func VerifyChecksumFile(root string) (bool, []string) {
	fsys := NewOSFileSystem()
	return VerifyChecksumFileWithFS(fsys, root)
}

// VerifyChecksumFileWithFS verifies CHECKSUM using the given FileSystem.
func VerifyChecksumFileWithFS(fsys FileSystem, root string) (bool, []string) {
	abs := fsys.Abs(root)
	ckPath := fsys.Join(abs, ChecksumFileName)
	data, err := fsys.ReadFile(ckPath)
	if err != nil {
		return false, []string{ChecksumFileName + " not found"}
	}

	declared := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) == 2 {
			declared[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}

	var errors []string
	_ = fsys.Walk(abs, func(path string, info fs.FileInfo, err error) error {
		if err != nil || info.IsDir() || fsys.Base(path) == ChecksumFileName {
			return nil
		}
		name := fsys.Base(path)
		ext := fsys.Ext(path)

		if name == "SKILL.md" {
			// Verify SKILL.md checksum too (consistent with generation)
			rel, _ := fsys.Rel(abs, path)
			line := computeSkillChecksumWithFS(fsys, path, rel)
			if line != "" {
				parts := strings.SplitN(line, "  ", 2)
				if len(parts) == 2 {
					defKey := strings.TrimSpace(parts[0])
					actualHash := strings.TrimSpace(parts[1])
					if decl, ok := declared[defKey]; ok {
						if decl != actualHash {
							errors = append(errors, "Mismatch: "+defKey)
						}
						delete(declared, defKey)
					} else {
						errors = append(errors, "Unexpected definition: "+defKey)
					}
				}
			}
		} else if ext == ".bkn" {
			lines := computeBknChecksumWithFS(fsys, path)
			for _, line := range lines {
				parts := strings.SplitN(line, "  ", 2)
				if len(parts) == 2 {
					defKey := strings.TrimSpace(parts[0])
					actualHash := strings.TrimSpace(parts[1])
					if decl, ok := declared[defKey]; ok {
						if decl != actualHash {
							errors = append(errors, "Mismatch: "+defKey)
						}
						delete(declared, defKey)
					} else {
						errors = append(errors, "Unexpected definition: "+defKey)
					}
				}
			}
		}
		return nil
	})

	for defKey := range declared {
		if defKey != "*" {
			errors = append(errors, "Missing definition: "+defKey)
		}
	}

	return len(errors) == 0, errors
}

func computeSkillChecksumWithFS(fsys FileSystem, path, rel string) string {
	data, err := fsys.ReadFile(path)
	if err != nil {
		return ""
	}
	norm := normalizeForChecksum(string(data))
	return rel + "  sha256:" + hashHex([]byte(norm))
}

// computeBknChecksumWithFS computes checksums for all definitions in a .bkn file.
// Format per DESIGN.md:
//   - network type (no id suffix): "network  sha256:..."
//   - definition types: "object_type:id  sha256:..."
func computeBknChecksumWithFS(fsys FileSystem, path string) []string {
	data, err := fsys.ReadFile(path)
	if err != nil {
		return nil
	}
	content := string(data)

	fm, err := ParseFrontmatter(content)
	if err != nil {
		return nil
	}

	var results []string
	typeValRaw, typeOk := fm["type"].(string)
	if !typeOk {
		return nil
	}
	typeVal := strings.TrimSpace(typeValRaw)

	if fm["id"] == nil {
		return nil
	}
	id := strings.TrimSpace(fmt.Sprintf("%v", fm["id"]))

	// For network type, use "network" (no :id suffix per DESIGN.md)
	if typeVal == "network" {
		_, body := splitFrontmatter(content)
		norm := normalizeForChecksum(body)
		results = append(results, "network  sha256:"+hashHex([]byte(norm)))
		return results
	}

	// For definition types, compute checksum based on type and id
	_, body := splitFrontmatter(content)
	norm := normalizeForChecksum(body)

	switch typeVal {
	case "object_type":
		results = append(results, "object_type:"+id+"  sha256:"+hashHex([]byte(norm)))
	case "relation_type":
		results = append(results, "relation_type:"+id+"  sha256:"+hashHex([]byte(norm)))
	case "action_type":
		results = append(results, "action_type:"+id+"  sha256:"+hashHex([]byte(norm)))
	case "risk_type":
		results = append(results, "risk_type:"+id+"  sha256:"+hashHex([]byte(norm)))
	case "concept_group":
		results = append(results, "concept_group:"+id+"  sha256:"+hashHex([]byte(norm)))
	case "metric":
		results = append(results, "metric:"+id+"  sha256:"+hashHex([]byte(norm)))
	}

	return results
}

// normalizeForChecksum normalizes text before hashing so that blank lines,
// CRLF/LF differences, trailing whitespace, and table-cell padding do not
// affect the checksum. Semantic content changes still change the checksum.
func normalizeForChecksum(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, "\n")
}
