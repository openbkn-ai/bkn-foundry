// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package capability follows the functions (toolboxes) and Skills a network
// uses from one release to the next. Each is kept in git as it was released,
// release.yaml records its digest, and publishing reuses an unchanged one
// under the same id or copies it under a new id for the new network.
package capability

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const (
	// TOOLBOX_FILE holds the impex export of a toolbox in its directory.
	TOOLBOX_FILE = "toolbox.json"
	// SKILL_MD is the entry file of a Skill package.
	SKILL_MD = "SKILL.md"
)

// Kind is what an item is.
type Kind string

const (
	KIND_FUNCTION Kind = "function"
	KIND_SKILL    Kind = "skill"
)

// Item is one toolbox or Skill as git keeps it: Files are keyed by the path
// inside the item's directory, and Digest covers all of them.
type Item struct {
	Kind   Kind
	ID     string
	Name   string
	Files  map[string][]byte
	Digest string
}

// Dir is the directory of the item from the repository root.
func (it *Item) Dir() string {
	if it.Kind == KIND_FUNCTION {
		return interfaces.GIT_FUNCTIONS_DIR + "/" + it.ID
	}
	return interfaces.GIT_SKILLS_DIR + "/" + it.ID
}

func newItem(kind Kind, id string, name string, files map[string][]byte) (*Item, error) {
	// The id names a directory in git.
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\\x00") {
		return nil, fmt.Errorf("%s id %q cannot name a directory", kind, id)
	}
	return &Item{Kind: kind, ID: id, Name: name, Files: files, Digest: Digest(files)}, nil
}

// NewFunction reads a toolbox impex export into an item. The export is
// stored with sorted keys and tools, and without category_name, which
// execution-factory localizes to the caller's language, so the same toolbox
// always has the same digest.
func NewFunction(export []byte) (*Item, error) {
	doc, err := decodeJSON(export)
	if err != nil {
		return nil, fmt.Errorf("read toolbox export: %w", err)
	}
	box, err := toolboxConfig(doc)
	if err != nil {
		return nil, err
	}
	delete(box, "category_name")
	if tools, ok := box["tools"].([]any); ok {
		sort.SliceStable(tools, func(i, j int) bool {
			return stringField(tools[i], "tool_id") < stringField(tools[j], "tool_id")
		})
	}
	data, err := encodeJSON(doc)
	if err != nil {
		return nil, err
	}
	return newItem(KIND_FUNCTION, stringField(box, "box_id"), stringField(box, "box_name"),
		map[string][]byte{TOOLBOX_FILE: data})
}

// NewSkill makes an item of the files of a released Skill; its name is the
// name in the frontmatter of SKILL.md.
func NewSkill(id string, files map[string][]byte) (*Item, error) {
	text, ok := files[SKILL_MD]
	if !ok {
		return nil, fmt.Errorf("skill %s has no %s", id, SKILL_MD)
	}
	name := ""
	_, err := common.EditFrontmatter(string(text), SKILL_MD, func(root *yaml.Node, _ common.FrontmatterSetter) error {
		if _, v := common.MappingValue(root, "name"); v != nil {
			name = v.Value
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("skill %s: %w", id, err)
	}
	return newItem(KIND_SKILL, id, name, files)
}

// Digest is "sha256:" and the hash of every path with the hash of its content, in path order.
func Digest(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		sum := sha256.Sum256(files[p])
		fmt.Fprintf(h, "%s\x00%s\n", p, hex.EncodeToString(sum[:]))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// Files lays the items out as git keeps them, keyed by path from the repository root.
func Files(items []*Item) map[string][]byte {
	files := map[string][]byte{}
	for _, it := range items {
		for p, data := range it.Files {
			files[path.Join(it.Dir(), p)] = data
		}
	}
	return files
}

// StoredFiles drops release.yaml from what ReadCapabilities returned, leaving the item files.
func StoredFiles(capabilities map[string][]byte) map[string][]byte {
	files := make(map[string][]byte, len(capabilities))
	for p, data := range capabilities {
		if p != interfaces.GIT_MANIFEST_FILE {
			files[p] = data
		}
	}
	return files
}

// Manifest is release.yaml: the functions and Skills of the latest release,
// with the digest each had when it was released and the networks of the
// lineage that use it under its id.
type Manifest struct {
	Functions []*ManifestEntry `yaml:"functions"`
	Skills    []*ManifestEntry `yaml:"skills"`
}

type ManifestEntry struct {
	ID       string   `yaml:"id"`
	Name     string   `yaml:"name"`
	Digest   string   `yaml:"digest"`
	Networks []string `yaml:"networks"`
}

// ParseManifest reads release.yaml; no file is an empty manifest.
func ParseManifest(data []byte) (*Manifest, error) {
	m := &Manifest{}
	if len(bytes.TrimSpace(data)) == 0 {
		return m, nil
	}
	if err := yaml.Unmarshal(data, m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", interfaces.GIT_MANIFEST_FILE, err)
	}
	return m, nil
}

func (m *Manifest) Marshal() ([]byte, error) {
	return yaml.Marshal(m)
}

// Empty reports whether the manifest lists nothing.
func (m *Manifest) Empty() bool {
	return len(m.Functions) == 0 && len(m.Skills) == 0
}

// Keep returns the manifest with only the entries of items, so an item that
// no longer exists drops out of it.
func (m *Manifest) Keep(items []*Item) *Manifest {
	kept := &Manifest{Functions: []*ManifestEntry{}, Skills: []*ManifestEntry{}}
	for _, it := range items {
		e := m.Entry(it.Kind, it.ID)
		switch {
		case e == nil:
		case it.Kind == KIND_FUNCTION:
			kept.Functions = append(kept.Functions, e)
		default:
			kept.Skills = append(kept.Skills, e)
		}
	}
	return kept
}

// Entry returns the entry of an item, or nil.
func (m *Manifest) Entry(kind Kind, id string) *ManifestEntry {
	entries := m.Skills
	if kind == KIND_FUNCTION {
		entries = m.Functions
	}
	for _, e := range entries {
		if e.ID == id {
			return e
		}
	}
	return nil
}

func decodeJSON(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	// Numbers stay as written: timestamps in nanoseconds do not survive float64.
	dec.UseNumber()
	doc := map[string]any{}
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func encodeJSON(doc any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// Function code is stored as written, < > & included.
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// toolboxConfig returns the one toolbox of a toolbox export.
func toolboxConfig(doc map[string]any) (map[string]any, error) {
	toolbox, _ := doc["toolbox"].(map[string]any)
	configs, _ := toolbox["configs"].([]any)
	if len(configs) != 1 {
		return nil, fmt.Errorf("toolbox export holds %d toolboxes, not 1", len(configs))
	}
	box, ok := configs[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("toolbox export config is not an object")
	}
	return box, nil
}

func stringField(v any, key string) string {
	m, _ := v.(map[string]any)
	s, _ := m[key].(string)
	return s
}
