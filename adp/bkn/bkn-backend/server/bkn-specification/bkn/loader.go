// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Root file discovery: network.bkn
const (
	RootFileName     = "network.bkn"
	ChecksumFileName = "CHECKSUM"
	SkillFileName    = "SKILL.md"
	ExtensionBkn     = ".bkn"
)

// valueTypesDir holds the value types of a 3.0 package (SPECIFICATION.md §16). It is not one of
// SupportedBknSubDirs: the 2.x reader never looks into it.
const valueTypesDir = "value_types"

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
		"object_metrics": true,
	}
)

// LoadNetwork loads a BKN network from a directory.
// It reads network.bkn and SKILL.md from the root, then traverses
// object_types/, relation_types/, action_types/, risk_types/, concept_groups/, object_metrics/
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
	return loadNetworkWithFS(fsys, rootPath, true)
}

// LoadNetworkDefinitionsWithFS loads a BKN network like LoadNetworkWithFS, but leaves unchecked the
// 3.0 declarations that span definitions (ValidateNetworkDeclarations). Every file is still parsed
// and checked under the specification network.bkn declares. It is for a caller that puts a network
// together from several sources, such as a merge, and reports those declarations itself.
func LoadNetworkDefinitionsWithFS(fsys FileSystem, rootPath string) (*BknNetwork, error) {
	return loadNetworkWithFS(fsys, rootPath, false)
}

func loadNetworkWithFS(fsys FileSystem, rootPath string, checkDeclarations bool) (*BknNetwork, error) {
	absRoot := fsys.Abs(rootPath)

	// Ensure rootPath is a directory
	if !fsys.IsDir(absRoot) {
		return nil, fmt.Errorf("root path must be a directory: %s", absRoot)
	}

	// Step 1: Verify CHECKSUM if exists
	checksumFile := fsys.Join(absRoot, ChecksumFileName)
	checksumValid := true
	if _, err := fsys.Stat(checksumFile); err == nil {
		// CHECKSUM exists, validate it
		ok, errMsgs := VerifyChecksumFileWithFS(fsys, absRoot)
		checksumValid = ok
		if !ok {
			// Report mismatches now; object metric V1 archives fail after loading.
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

	// Step 4: Traverse subdirectories and load definitions, under the rules of the specification
	// network.bkn declares.
	opts := parseOptions{spec: bknDoc.BknSpec}
	if opts.v3() && fsys.IsDir(fsys.Join(absRoot, valueTypesDir)) {
		if err := loadSubdirWithFS(fsys, fsys.Join(absRoot, valueTypesDir), valueTypesDir, bknDoc, opts); err != nil {
			return nil, fmt.Errorf("load %s: %w", valueTypesDir, err)
		}
	}
	for subdir := range SupportedBknSubDirs {
		subdirPath := fsys.Join(absRoot, subdir)
		if !fsys.IsDir(subdirPath) {
			continue
		}

		if err := loadSubdirWithFS(fsys, subdirPath, subdir, bknDoc, opts); err != nil {
			return nil, fmt.Errorf("load %s: %w", subdir, err)
		}
	}
	if fsys.IsDir(fsys.Join(absRoot, "metrics")) {
		return nil, fmt.Errorf("legacy metrics/*.bkn is unsupported; migrate metrics to object metrics V1 before import")
	}
	if opts.v3() && checkDeclarations {
		if err := ValidateNetworkDeclarations(bknDoc); err != nil {
			return nil, err
		}
	}
	if len(bknDoc.ObjectMetrics) > 0 && !checksumValid {
		return nil, fmt.Errorf("object metric V1 archive checksum verification failed")
	}
	sort.Slice(bknDoc.ObjectMetrics, func(i, j int) bool {
		leftID, leftVersion, _, _ := objectMetricArchiveMetadata(bknDoc.ObjectMetrics[i])
		rightID, rightVersion, _, _ := objectMetricArchiveMetadata(bknDoc.ObjectMetrics[j])
		if leftID != rightID {
			return leftID < rightID
		}
		return leftVersion < rightVersion
	})

	return bknDoc, nil
}

// loadSubdirWithFS loads all .bkn/.md files from a subdirectory into the result document.
// It uses the subdirectory name to determine which type-specific parser to use.
func loadSubdirWithFS(fsys FileSystem, subdirPath, subdirName string, result *BknNetwork, opts parseOptions) error {
	entries, err := fsys.ReadDir(subdirPath)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			if subdirName == "object_metrics" {
				return fmt.Errorf("nested object metric directory %q is unsupported", entry.Name())
			}
			continue
		}

		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if subdirName == "object_metrics" {
			if ext != ".json" {
				return fmt.Errorf("object metric file %q must be JSON", name)
			}
			filePath := fsys.Join(subdirPath, name)
			data, err := fsys.ReadFile(filePath)
			if err != nil {
				return err
			}
			id, version, err := objectMetricArchiveIdentity(data)
			if err != nil {
				return fmt.Errorf("parse %s: %w", name, err)
			}
			if name != objectMetricArchiveFileName(id, version) {
				return fmt.Errorf("object metric file %q does not match its id and version", name)
			}
			result.ObjectMetrics = append(result.ObjectMetrics, json.RawMessage(data))
			continue
		}
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
			obj, err := parseObjectTypeFile(string(data), filePath, opts)
			if err != nil {
				return fmt.Errorf("parse %s: %w", name, err)
			}
			result.ObjectTypes = append(result.ObjectTypes, obj)

		case "relation_types":
			rel, err := parseRelationTypeFile(string(data), filePath, opts)
			if err != nil {
				return fmt.Errorf("parse %s: %w", name, err)
			}
			result.RelationTypes = append(result.RelationTypes, rel)

		case "action_types":
			act, err := parseActionTypeFile(string(data), filePath, opts)
			if err != nil {
				return fmt.Errorf("parse %s: %w", name, err)
			}
			result.ActionTypes = append(result.ActionTypes, act)

		case "risk_types":
			ris, err := parseRiskTypeFile(string(data), filePath, opts)
			if err != nil {
				return fmt.Errorf("parse %s: %w", name, err)
			}
			result.RiskTypes = append(result.RiskTypes, ris)

		case valueTypesDir:
			vt, err := parseValueTypeFile(string(data))
			if err != nil {
				return fmt.Errorf("parse %s: %w", name, err)
			}
			result.ValueTypes = append(result.ValueTypes, vt)

		case "concept_groups":
			// Concept groups use generic parsing for now
			grp, err := parseConceptGroupFile(string(data), filePath, opts)
			if err != nil {
				return fmt.Errorf("parse %s: %w", name, err)
			}
			result.ConceptGroups = append(result.ConceptGroups, grp)

		default:
			// Fallback to generic Parse for unknown subdirectories
			return fmt.Errorf("unknown subdirectory type: %s", subdirName)
		}
	}

	return nil
}
