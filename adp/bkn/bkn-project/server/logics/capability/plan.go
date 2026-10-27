// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package capability

import (
	"bytes"
	"slices"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
)

// Why an item is copied.
const (
	// REASON_CHANGED: its digest differs from the one the previous release recorded.
	REASON_CHANGED = "changed"
	// REASON_NETWORK_ID: it names the old network id, which the copy names the new one instead.
	REASON_NETWORK_ID = "network_id"
)

// Step is what a release does with one item: reuse it under its id, or copy it.
type Step struct {
	Item    *Item
	Copy    bool
	Reasons []string
}

// References lists the toolboxes and Skills a network uses: the toolboxes of
// tool action sources, of tool logic properties and of the functions
// capabilities, and the Skills of the skills capabilities. Capabilities that
// only name their target are bound by name and are not followed.
func References(network *bkn.BknNetwork) (boxIDs []string, skillIDs []string) {
	boxes, skills := map[string]bool{}, map[string]bool{}
	for _, at := range network.ActionTypes {
		if s := at.ActionSource; s != nil && strings.EqualFold(s.Type, "tool") && s.BoxID != "" {
			boxes[s.BoxID] = true
		}
	}
	for _, ot := range network.ObjectTypes {
		for _, lp := range ot.LogicProperties {
			if s := lp.DataSource; s != nil && strings.EqualFold(s.Type, "tool") && s.BoxID != "" {
				boxes[s.BoxID] = true
			}
		}
	}
	if c := network.Capabilities; c != nil {
		for _, f := range c.Functions {
			if f.BoxID != "" {
				boxes[f.BoxID] = true
			}
		}
		for _, s := range c.Skills {
			if s.ID != "" {
				skills[s.ID] = true
			}
		}
	}
	return sortedKeys(boxes), sortedKeys(skills)
}

// Plan decides per item: an item whose digest the previous release recorded
// differently, or that names oldNetworkID, is copied; any other is reused.
// An item the previous release did not record has nothing to differ from.
func Plan(previous *Manifest, items []*Item, oldNetworkID string) []*Step {
	steps := make([]*Step, 0, len(items))
	for _, it := range items {
		step := &Step{Item: it}
		if e := previous.Entry(it.Kind, it.ID); e != nil && e.Digest != it.Digest {
			step.Reasons = append(step.Reasons, REASON_CHANGED)
		}
		if mentionsNetworkID(it.Files, oldNetworkID) {
			step.Reasons = append(step.Reasons, REASON_NETWORK_ID)
		}
		step.Copy = len(step.Reasons) > 0
		steps = append(steps, step)
	}
	return steps
}

// NextManifest records the items the new network uses. An item kept under an
// id the previous release recorded adds the new network to that entry's
// networks; any other starts with the new network alone.
func NextManifest(previous *Manifest, items []*Item, networkID string) *Manifest {
	next := &Manifest{Functions: []*ManifestEntry{}, Skills: []*ManifestEntry{}}
	for _, it := range items {
		networks := []string{}
		if e := previous.Entry(it.Kind, it.ID); e != nil {
			networks = append(networks, e.Networks...)
		}
		if !slices.Contains(networks, networkID) {
			networks = append(networks, networkID)
		}
		entry := &ManifestEntry{ID: it.ID, Name: it.Name, Digest: it.Digest, Networks: networks}
		if it.Kind == KIND_FUNCTION {
			next.Functions = append(next.Functions, entry)
		} else {
			next.Skills = append(next.Skills, entry)
		}
	}
	sortEntries := func(entries []*ManifestEntry) {
		sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	}
	sortEntries(next.Functions)
	sortEntries(next.Skills)
	return next
}

// isIDByte tells the bytes a network id is made of, letters of either case
// included: a network id inside a longer word is not that network id.
func isIDByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-'
}

// networkIDIndexes returns where id stands as a whole word in text.
func networkIDIndexes(text []byte, id string) []int {
	if id == "" {
		return nil
	}
	var found []int
	for from := 0; from <= len(text)-len(id); {
		i := bytes.Index(text[from:], []byte(id))
		if i < 0 {
			break
		}
		i += from
		end := i + len(id)
		if (i == 0 || !isIDByte(text[i-1])) && (end == len(text) || !isIDByte(text[end])) {
			found = append(found, i)
		}
		from = i + 1
	}
	return found
}

func mentionsNetworkID(files map[string][]byte, id string) bool {
	for _, data := range files {
		if len(networkIDIndexes(data, id)) > 0 {
			return true
		}
	}
	return false
}

// replaceNetworkID rewrites every whole-word oldID in text to newID.
func replaceNetworkID(text []byte, oldID string, newID string) []byte {
	indexes := networkIDIndexes(text, oldID)
	if len(indexes) == 0 {
		return text
	}
	var out strings.Builder
	last := 0
	for _, i := range indexes {
		out.Write(text[last:i])
		out.WriteString(newID)
		last = i + len(oldID)
	}
	out.Write(text[last:])
	return []byte(out.String())
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
