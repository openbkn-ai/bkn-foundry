// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Root file discovery: network.bkn
const (
	RootFileName     = "network.bkn"
	ChecksumFileName = "CHECKSUM"
	SkillFileName    = "SKILL.md"
	ExtensionBkn     = ".bkn"
)

// Supported file extensions for BKN content.
var (
	SupportedExtensions = map[string]bool{
		".bkn": true,
		".md":  true,
	}
	// SupportedBknSubDirs defines the standard subdirectories for BKN definitions.
	SupportedBknSubDirs = map[string]bool{
		"object_types":   true,
		"relation_types": true,
		"action_types":   true,
		"risk_types":     true,
		"concept_groups": true,
		"metrics":        true,
	}
)

// LoadNetwork loads a BKN network from a directory.
// It reads network.bkn and SKILL.md from the root, then traverses
// object_types/, relation_types/, action_types/, risk_types/, concept_groups/, metrics/
// to build a complete BknNetwork.
func LoadNetwork(rootPath string) (*BknNetwork, error) {
	fsys := NewOSFileSystem()
	return LoadNetworkWithFS(fsys, rootPath)
}

// LoadNetworkWithFS loads a BKN network using the specified filesystem.
// rootPath should be a directory containing network.bkn and standard subdirectories.
// If CHECKSUM file exists, it will be validated against the actual file contents.
// Checksum validation failures are logged as warnings but do not prevent loading.
func LoadNetworkWithFS(fsys FileSystem, rootPath string) (*BknNetwork, error) {
	absRoot := fsys.Abs(rootPath)

	// Ensure rootPath is a directory
	if !fsys.IsDir(absRoot) {
		return nil, fmt.Errorf("root path must be a directory: %s", absRoot)
	}

	// Step 1: Verify CHECKSUM if exists
	checksumFile := fsys.Join(absRoot, ChecksumFileName)
	if _, err := fsys.Stat(checksumFile); err == nil {
		// CHECKSUM exists, validate it
		ok, errMsgs := VerifyChecksumFileWithFS(fsys, absRoot)
		if !ok {
			// Log warnings for checksum mismatches but continue loading
			fmt.Fprintf(os.Stderr, "[WARN] CHECKSUM validation failed for %s:\n", absRoot)
			for _, msg := range errMsgs {
				fmt.Fprintf(os.Stderr, "  - %s\n", msg)
			}
		}
	}

	// Step 2: Load network.bkn for frontmatter
	networkFile := fsys.Join(absRoot, RootFileName)
	if _, err := fsys.Stat(networkFile); err != nil {
		return nil, fmt.Errorf("network.bkn not found in %s", absRoot)
	}

	data, err := fsys.ReadFile(networkFile)
	if err != nil {
		return nil, err
	}

	bknDoc, err := ParseNetworkFile(string(data), absRoot)
	if err != nil {
		return nil, fmt.Errorf("load network.bkn: %w", err)
	}

	// Step 3: Load SKILL.md if exists
	skillFile := fsys.Join(absRoot, SkillFileName)
	if _, err := fsys.Stat(skillFile); err == nil {
		skillData, err := fsys.ReadFile(skillFile)
		if err != nil {
			return nil, fmt.Errorf("read SKILL.md: %w", err)
		}
		bknDoc.SkillContent = string(skillData)
	}

	// Step 4: Traverse subdirectories and load definitions
	for subdir := range SupportedBknSubDirs {
		subdirPath := fsys.Join(absRoot, subdir)
		if !fsys.IsDir(subdirPath) {
			continue
		}

		if err := loadSubdirWithFS(fsys, subdirPath, subdir, bknDoc); err != nil {
			return nil, fmt.Errorf("load %s: %w", subdir, err)
		}
	}

	return bknDoc, nil
}

// loadSubdirWithFS loads all .bkn/.md files from a subdirectory into the result document.
// It uses the subdirectory name to determine which type-specific parser to use.
func loadSubdirWithFS(fsys FileSystem, subdirPath, subdirName string, result *BknNetwork) error {
	entries, err := fsys.ReadDir(subdirPath)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ExtensionBkn {
			continue
		}

		filePath := fsys.Join(subdirPath, name)
		data, err := fsys.ReadFile(filePath)
		if err != nil {
			return err
		}

		// Use type-specific parser based on subdirectory type
		switch subdirName {
		case "object_types":
			obj, err := ParseObjectTypeFile(string(data), filePath)
			if err != nil {
				return fmt.Errorf("parse %s: %w", name, err)
			}
			result.ObjectTypes = append(result.ObjectTypes, obj)

		case "relation_types":
			rel, err := ParseRelationTypeFile(string(data), filePath)
			if err != nil {
				return fmt.Errorf("parse %s: %w", name, err)
			}
			result.RelationTypes = append(result.RelationTypes, rel)

		case "action_types":
			act, err := ParseActionTypeFile(string(data), filePath)
			if err != nil {
				return fmt.Errorf("parse %s: %w", name, err)
			}
			result.ActionTypes = append(result.ActionTypes, act)

		case "risk_types":
			ris, err := ParseRiskTypeFile(string(data), filePath)
			if err != nil {
				return fmt.Errorf("parse %s: %w", name, err)
			}
			result.RiskTypes = append(result.RiskTypes, ris)

		case "concept_groups":
			// Concept groups use generic parsing for now
			grp, err := ParseConceptGroupFile(string(data), filePath)
			if err != nil {
				return fmt.Errorf("parse %s: %w", name, err)
			}
			result.ConceptGroups = append(result.ConceptGroups, grp)

		case "metrics":
			me, err := ParseMetricFile(string(data), filePath)
			if err != nil {
				return fmt.Errorf("parse %s: %w", name, err)
			}
			result.Metrics = append(result.Metrics, me)

		default:
			// Fallback to generic Parse for unknown subdirectories
			return fmt.Errorf("unknown subdirectory type: %s", subdirName)
		}
	}

	return nil
}
