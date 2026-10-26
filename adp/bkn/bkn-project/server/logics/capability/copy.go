// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package capability

import (
	"archive/zip"
	"bytes"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
)

// toolboxNameLimit is the longest toolbox name execution-factory accepts, in characters.
const toolboxNameLimit = 50

// copyNamespace derives the ids of copies, so a retried release finds the
// copies its failed attempt made instead of making more.
var copyNamespace = uuid.MustParse("6b1f0e52-3c8e-4d0b-9a57-2f3c9d1e7a40")

// Copy is an item copied for a new network, and the ids the network's
// references move to.
type Copy struct {
	Kind    Kind
	OldID   string
	NewID   string
	NewName string
	// ToolIDs maps the tool ids of a copied toolbox to the copy's.
	ToolIDs map[string]string
}

// copyID is the id a copy of id gets for networkID: the same on every attempt.
func copyID(id string, networkID string) string {
	return uuid.NewSHA1(copyNamespace, []byte(networkID+"\x00"+id)).String()
}

// copyName is <name>_<new network id>, with "-" written as "_" since toolbox
// names take word characters only. The suffix an earlier copy for the old
// network appended is replaced, not appended to. limit, when not 0, cuts the
// name to that many characters.
func copyName(name string, oldNetworkID string, newNetworkID string, limit int) string {
	word := func(id string) string { return "_" + strings.ReplaceAll(id, "-", "_") }
	base, suffix := strings.TrimSuffix(name, word(oldNetworkID)), word(newNetworkID)
	if keep := limit - utf8.RuneCountInString(suffix); limit > 0 && utf8.RuneCountInString(base) > keep {
		base = string([]rune(base)[:keep])
	}
	return base + suffix
}

// CopyFunction turns the export of a toolbox into the import of its copy for
// newNetworkID: new box and tool ids, a new name and oldNetworkID rewritten to
// newNetworkID wherever it stands as a word. The operators that operator tools
// run are left out: the copy's tools run the same operators, which keep their ids.
func CopyFunction(item *Item, oldNetworkID string, newNetworkID string) ([]byte, *Copy, error) {
	doc, err := decodeJSON(replaceNetworkID(item.Files[TOOLBOX_FILE], oldNetworkID, newNetworkID))
	if err != nil {
		return nil, nil, fmt.Errorf("read toolbox %s: %w", item.ID, err)
	}
	box, err := toolboxConfig(doc)
	if err != nil {
		return nil, nil, err
	}
	c := &Copy{
		Kind:    KIND_FUNCTION,
		OldID:   item.ID,
		NewID:   copyID(item.ID, newNetworkID),
		NewName: copyName(item.Name, oldNetworkID, newNetworkID, toolboxNameLimit),
		ToolIDs: map[string]string{},
	}
	box["box_id"], box["box_name"] = c.NewID, c.NewName
	tools, _ := box["tools"].([]any)
	for _, tool := range tools {
		t, ok := tool.(map[string]any)
		if !ok {
			continue
		}
		id, _ := t["tool_id"].(string)
		c.ToolIDs[id] = copyID(id, newNetworkID)
		t["tool_id"] = c.ToolIDs[id]
	}
	delete(doc, "operator")
	data, err := encodeJSON(doc)
	if err != nil {
		return nil, nil, err
	}
	return data, c, nil
}

// CopySkill turns the files of a released Skill into those of its copy for
// newNetworkID: oldNetworkID rewritten to newNetworkID wherever it stands as
// a word in a text file, and a new name in SKILL.md, since a published name
// must be unique. NewID is left for registration to fill in.
func CopySkill(item *Item, oldNetworkID string, newNetworkID string) (map[string][]byte, *Copy, error) {
	c := &Copy{Kind: KIND_SKILL, OldID: item.ID, NewName: copyName(item.Name, oldNetworkID, newNetworkID, 0)}
	files := make(map[string][]byte, len(item.Files))
	for p, data := range item.Files {
		if utf8.Valid(data) {
			data = replaceNetworkID(data, oldNetworkID, newNetworkID)
		}
		files[p] = data
	}
	text, err := common.EditFrontmatter(string(files[SKILL_MD]), SKILL_MD, func(root *yaml.Node, set common.FrontmatterSetter) error {
		k, v := common.MappingValue(root, "name")
		if k == nil {
			return fmt.Errorf("%s frontmatter has no name", SKILL_MD)
		}
		return set(k, v, c.NewName)
	})
	if err != nil {
		return nil, nil, fmt.Errorf("skill %s: %w", item.ID, err)
	}
	files[SKILL_MD] = []byte(text)
	return files, c, nil
}

// SkillArchive packs the files of a Skill into the zip registration takes.
func SkillArchive(files map[string][]byte) ([]byte, error) {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range paths {
		w, err := zw.Create(p)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(files[p]); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
