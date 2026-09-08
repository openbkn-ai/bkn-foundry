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
	"strconv"
	"strings"
	"time"
)

// hashHex computes SHA-256 and returns the first 8 bytes as 16 hex chars.
func hashHex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:8])
}

const (
	// checksumFormatBodyOnly is the original layout: a definition's checksum covered its body and
	// nothing else.
	checksumFormatBodyOnly = 1
	// checksumFormatWithFrontmatter also covers the frontmatter.
	checksumFormatWithFrontmatter = 2
	// checksumFormatCurrent is what new CHECKSUM files declare and what diffing uses.
	checksumFormatCurrent = checksumFormatWithFrontmatter

	checksumFormatPrefix = "# format:"
)

// checksumPayload builds the bytes a definition's checksum is taken over.
//
// Format 1 hashed the body alone, leaving the whole frontmatter outside the checksum: renaming a
// definition, retagging it, or rebinding a capability produced a byte-identical hash, so a diff
// built on these checksums reported "unchanged" for a file that had visibly changed. Format 2
// folds the frontmatter in. CHECKSUM files written before this change declare no format and are
// still verified under format 1, so existing packages keep verifying.
func checksumPayload(fm map[string]any, body string, format int) string {
	norm := normalizeForChecksum(body)
	if format < checksumFormatWithFrontmatter {
		return norm
	}
	return canonicalFrontmatter(fm) + "\n" + norm
}

// canonicalFrontmatter renders parsed frontmatter deterministically. Map keys are sorted at every
// level: Go map iteration order is random, and without sorting the same file would hash
// differently from one run to the next.
func canonicalFrontmatter(fm map[string]any) string {
	return canonicalValue(fm, "")
}

func canonicalValue(v any, indent string) string {
	switch val := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var sb strings.Builder
		for _, k := range keys {
			sb.WriteString(indent)
			sb.WriteString(k)
			if isCompoundValue(val[k]) {
				sb.WriteString(":\n")
				sb.WriteString(canonicalValue(val[k], indent+"  "))
				continue
			}
			sb.WriteString(": ")
			sb.WriteString(scalarString(val[k]))
			sb.WriteString("\n")
		}
		return sb.String()
	case map[any]any:
		// Defensive: some YAML decoders hand back interface-keyed maps.
		converted := make(map[string]any, len(val))
		for k, item := range val {
			converted[fmt.Sprint(k)] = item
		}
		return canonicalValue(converted, indent)
	case []any:
		var sb strings.Builder
		for _, item := range val {
			if isCompoundValue(item) {
				sb.WriteString(indent)
				sb.WriteString("-\n")
				sb.WriteString(canonicalValue(item, indent+"  "))
				continue
			}
			sb.WriteString(indent)
			sb.WriteString("- ")
			sb.WriteString(scalarString(item))
			sb.WriteString("\n")
		}
		return sb.String()
	default:
		return indent + scalarString(v) + "\n"
	}
}

func isCompoundValue(v any) bool {
	switch v.(type) {
	case map[string]any, map[any]any, []any:
		return true
	}
	return false
}

func scalarString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// declaredChecksumFormat reads the format a CHECKSUM file was written under. A file without the
// header predates format 2 and is verified as format 1.
func declaredChecksumFormat(content string) int {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, checksumFormatPrefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, checksumFormatPrefix))
		if n, err := strconv.Atoi(value); err == nil {
			return n
		}
	}
	return checksumFormatBodyOnly
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
			lines := computeBknChecksumWithFS(fsys, path, checksumFormatCurrent)
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
		fmt.Sprintf("%s %d", checksumFormatPrefix, checksumFormatCurrent),
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

	format := declaredChecksumFormat(string(data))
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
			lines := computeBknChecksumWithFS(fsys, path, format)
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
func computeBknChecksumWithFS(fsys FileSystem, path string, format int) []string {
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

	// For network type, use "network" (no :id suffix per DESIGN.md).
	//
	// The serializer writes "knowledge_network"; only the legacy "network" spelling was matched
	// here, so the root file contributed no checksum line at all and every change to it — the
	// capability dependency block included — stayed outside both CHECKSUM and any diff built on
	// it. Accepting the real spelling is gated on format 2 so that CHECKSUM files written before
	// this change keep verifying without an "unexpected definition" complaint.
	if typeVal == "network" || (typeVal == "knowledge_network" && format >= checksumFormatWithFrontmatter) {
		_, body := splitFrontmatter(content)
		norm := checksumPayload(fm, body, format)
		results = append(results, "network  sha256:"+hashHex([]byte(norm)))
		return results
	}

	// For definition types, compute checksum based on type and id
	_, body := splitFrontmatter(content)
	norm := checksumPayload(fm, body, format)

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
