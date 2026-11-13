// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Drafting names the items it drafts in the project's content language. The names and descriptions
// it reads from the data catalog are those its semantic understanding gave, once per catalog for
// every project that uses it, in whatever language it ran in. When they are in the other language,
// a bkn-agent task Agent names the new items again in the project's: the catalog stays as it is,
// and a name it gave stays as an alias. Items the network already has stay as the network names
// them, and an item still named by its table or field reads in no language and is left as drafted.

const (
	// namingAgentID is the bkn-agent task Agent that names drafted items in the project's language.
	namingAgentID = "bkn-project-draft-naming"
	// namingBatchMax bounds the items one task names; an object type and its properties go together.
	namingBatchMax = 40
	// namingParallel bounds the tasks running at once for one draft.
	namingParallel = 4
	// namingDeadline bounds one task.
	namingDeadline = 3 * time.Minute
)

// namingPoll is how often a naming task is read until it ends; tests shorten it.
var namingPoll = 2 * time.Second

// Kinds of items the naming message carries.
const (
	namingObjectType   = "object_type"
	namingProperty     = "property"
	namingRelationType = "relation_type"
	namingValueType    = "value_type"
)

// textLanguage is the content language a text reads in by its script: Chinese when it holds a Han
// character, English when it holds a Latin letter, none otherwise, as for codes and numbers.
func textLanguage(text string) string {
	latin := false
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return interfaces.NAMING_LANGUAGE_ZH_CN
		}
		latin = latin || unicode.Is(unicode.Latin, r)
	}
	if latin {
		return interfaces.NAMING_LANGUAGE_EN_US
	}
	return ""
}

// inOtherLanguage tells whether a text reads in a language other than the one given.
func inOtherLanguage(language string, text string) bool {
	read := textLanguage(text)
	return read != "" && read != language
}

// namingItem is an item as the naming message carries it.
type namingItem struct {
	Key         string `json:"key"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Source is the physical name of its table or field.
	Source string `json:"source,omitempty"`
	// ObjectType is the name of the object type a property belongs to.
	ObjectType string `json:"object_type,omitempty"`
}

// namingInput is the message the naming Agent reads.
type namingInput struct {
	Language string       `json:"language"`
	Items    []namingItem `json:"items"`
}

// namingOutput is an item named again, as the Agent gives it.
type namingOutput struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// namingFormat is the JSON Schema of the names the Agent gives.
func namingFormat() map[string]any {
	str := func(max int) map[string]any { return map[string]any{"type": "string", "maxLength": max} }
	item := map[string]any{"type": "object", "additionalProperties": false,
		"required": []string{"key", "name", "description"},
		"properties": map[string]any{
			"key":         map[string]any{"type": "string"},
			"name":        str(interfaces.DRAFT_NAME_MAX_LENGTH),
			"description": str(interfaces.DRAFT_DESCRIPTION_MAX_LENGTH),
		}}
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"items"},
		"properties": map[string]any{"items": map[string]any{"type": "array", "items": item}}}
}

// namingTarget is where a name given lands: the item's name, aliases and description.
type namingTarget struct {
	name        *string
	aliases     *[]string
	description *string
	// renames and redescribes tell what was in the other language.
	renames     bool
	redescribes bool
}

// nameInLanguage names again, in the project's language, what the new tables drafted: the object
// types of those tables with their properties, the relation types an end of which they are, and the
// value types of code tables among them. Only a name or description read from the catalog in the
// other language is named again. A relation type drafted from a referring field is named again from
// its object type's and field's new names, as drafting names it. Drafting never fails for naming:
// without the Agent, or when it fails, the items keep the catalog's names.
func (s *draftService) nameInLanguage(ctx context.Context, caller interfaces.AccountInfo, items *interfaces.DraftItems,
	language string, newTables []*interfaces.VegaTable) {
	if s.ba == nil || language == "" {
		return
	}
	isNew := map[string]bool{}
	for _, t := range newTables {
		isNew[t.ID] = true
	}
	targets := map[string]*namingTarget{}
	var groups [][]namingItem
	add := func(group []namingItem, item namingItem, target *namingTarget, physical string) []namingItem {
		target.renames = *target.name != physical && inOtherLanguage(language, *target.name)
		target.redescribes = inOtherLanguage(language, *target.description)
		if !target.renames && !target.redescribes {
			return group
		}
		item.Name, item.Description = *target.name, truncateRunes(*target.description, interfaces.DRAFT_DESCRIPTION_MAX_LENGTH)
		targets[item.Key] = target
		return append(group, item)
	}

	newTypes := map[string]bool{}
	before := map[string]*interfaces.DraftObjectType{}
	for _, ot := range items.ObjectTypes {
		if ot.Existing || !isNew[ot.Table.ResourceID] {
			continue
		}
		newTypes[ot.ID] = true
		before[ot.ID] = snapshotNames(ot)
		physical := ot.Table.Name
		if table := tableOf(newTables, ot.Table.ResourceID); table != nil {
			physical = physicalName(table)
		}
		group := add(nil, namingItem{Key: "o:" + ot.ID, Kind: namingObjectType, Source: physical},
			&namingTarget{name: &ot.Name, aliases: &ot.Aliases, description: &ot.Description}, physical)
		for _, p := range ot.Properties {
			group = add(group, namingItem{Key: "p:" + ot.ID + ":" + p.Name, Kind: namingProperty, Source: p.MappedField,
				ObjectType: ot.Name}, &namingTarget{name: &p.DisplayName, aliases: &p.Aliases, description: &p.Description},
				p.MappedField)
		}
		if len(group) > 0 {
			groups = append(groups, group)
		}
	}
	// A relation type named from its source's referring field is named again from the new names.
	fromField := map[string]bool{}
	var relations []namingItem
	for _, rt := range items.RelationTypes {
		if rt.Existing || !newTypes[rt.Source] && !newTypes[rt.Target] {
			continue
		}
		if source := before[rt.Source]; source != nil && len(rt.Mapping) > 0 &&
			rt.Name == relationNameIn(language, source, rt.Mapping[0].SourceProperty, rt.ID) {
			fromField[rt.ID] = true
			if !inOtherLanguage(language, rt.Description) {
				continue
			}
		}
		physical := rt.ID
		if rt.LinkTable != nil {
			physical = rt.LinkTable.Name
		}
		relations = add(relations, namingItem{Key: "r:" + rt.ID, Kind: namingRelationType, Source: physical},
			&namingTarget{name: &rt.Name, aliases: &rt.Aliases, description: &rt.Description}, physical)
		if fromField[rt.ID] {
			targets["r:"+rt.ID].renames = false
		}
	}
	var values []namingItem
	for _, vt := range items.ValueTypes {
		if !isNew[vt.CodeTable.ResourceID] {
			continue
		}
		values = add(values, namingItem{Key: "v:" + vt.ID, Kind: namingValueType, Source: vt.CodeTable.Name},
			&namingTarget{name: &vt.Name, description: &vt.Description}, vt.CodeTable.Name)
	}
	for _, rest := range [][]namingItem{relations, values} {
		for len(rest) > 0 {
			n := min(len(rest), namingBatchMax)
			groups = append(groups, rest[:n])
			rest = rest[n:]
		}
	}
	if len(groups) == 0 {
		return
	}

	named := s.runNaming(ctx, caller, language, batchesOf(groups))
	applyNames(items, language, targets, named)
	for _, rt := range items.RelationTypes {
		if fromField[rt.ID] {
			if source := findObjectType(items, rt.Source); source != nil {
				rt.Name = relationNameIn(language, source, rt.Mapping[0].SourceProperty, rt.ID)
			}
		}
	}
}

// snapshotNames copies what names an object type's relations: its name and its properties' names.
func snapshotNames(ot *interfaces.DraftObjectType) *interfaces.DraftObjectType {
	copied := &interfaces.DraftObjectType{ID: ot.ID, Name: ot.Name}
	for _, p := range ot.Properties {
		copied.Properties = append(copied.Properties, &interfaces.DraftProperty{Name: p.Name,
			DisplayName: p.DisplayName, MappedField: p.MappedField})
	}
	return copied
}

func tableOf(tables []*interfaces.VegaTable, id string) *interfaces.VegaTable {
	for _, t := range tables {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// batchesOf joins groups into batches of at most namingBatchMax items; a larger group is a batch
// of its own, cut to the bound.
func batchesOf(groups [][]namingItem) [][]namingItem {
	var batches [][]namingItem
	var batch []namingItem
	for _, group := range groups {
		if len(batch) > 0 && len(batch)+len(group) > namingBatchMax {
			batches = append(batches, batch)
			batch = nil
		}
		for len(group) > namingBatchMax {
			batches = append(batches, group[:namingBatchMax])
			group = group[namingBatchMax:]
		}
		batch = append(batch, group...)
	}
	if len(batch) > 0 {
		batches = append(batches, batch)
	}
	return batches
}

// runNaming runs a task for each batch, a few at once, and gives what they named by key. A task that
// fails names nothing.
func (s *draftService) runNaming(ctx context.Context, caller interfaces.AccountInfo, language string,
	batches [][]namingItem) map[string]namingOutput {
	named := map[string]namingOutput{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, namingParallel)
	for _, batch := range batches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			out, err := s.nameBatch(ctx, caller, language, batch)
			if err != nil {
				otellog.LogWarn(ctx, fmt.Sprintf("Naming %d drafted items in %s failed: %v", len(batch), language, err))
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, o := range out {
				named[o.Key] = o
			}
		}()
	}
	wg.Wait()
	return named
}

// nameBatch runs the naming Agent on one batch and waits for its task up to the deadline.
func (s *draftService) nameBatch(ctx context.Context, caller interfaces.AccountInfo, language string,
	batch []namingItem) ([]namingOutput, error) {
	message, err := json.Marshal(namingInput{Language: language, Items: batch})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, namingDeadline)
	defer cancel()
	task, err := s.ba.RunTask(ctx, caller, &interfaces.BknAgentRunRequest{AgentID: namingAgentID,
		Message: string(message), ResponseFormat: namingFormat()})
	if err != nil {
		return nil, err
	}
	for !task.Terminal() {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("task %s did not finish: %w", task.TaskID, ctx.Err())
		case <-time.After(namingPoll):
		}
		if read, err := s.ba.GetTask(ctx, caller, task.TaskID); err == nil {
			task = read
		}
	}
	if task.Status != interfaces.BKN_AGENT_TASK_SUCCEEDED {
		return nil, fmt.Errorf("task %s failed: %s", task.TaskID, task.FailureDetail)
	}
	var out struct {
		Items []namingOutput `json:"items"`
	}
	if err := json.Unmarshal([]byte(task.Output), &out); err != nil {
		return nil, fmt.Errorf("task %s output: %w", task.TaskID, err)
	}
	return out.Items, nil
}

// applyNames puts the names given on their items. A name is taken only when it reads in the
// project's language, fits and names no other item of its kind already; the name it replaces
// becomes an alias where the item takes one. A description is taken when it reads in the language.
func applyNames(items *interfaces.DraftItems, language string, targets map[string]*namingTarget,
	named map[string]namingOutput) {
	taken := func(key string, name string) bool {
		switch key[0] {
		case 'o':
			return slices.ContainsFunc(items.ObjectTypes, func(ot *interfaces.DraftObjectType) bool { return ot.Name == name })
		case 'p':
			id, _, _ := strings.Cut(key[2:], ":")
			ot := findObjectType(items, id)
			return ot != nil && slices.ContainsFunc(ot.Properties, func(p *interfaces.DraftProperty) bool {
				return p.DisplayName == name || p.Name == name
			})
		}
		return false
	}
	keys := make([]string, 0, len(targets))
	for key := range targets {
		keys = append(keys, key)
	}
	// One order however the tasks ended: which of two items given one name takes it must not vary.
	slices.Sort(keys)
	for _, key := range keys {
		target, out := targets[key], named[key]
		if out.Key == "" {
			continue
		}
		if name := strings.TrimSpace(out.Name); target.renames && name != "" && name != *target.name &&
			!inOtherLanguage(language, name) && utf8.RuneCountInString(name) <= maxDisplayNameRunes && !taken(key, name) {
			old := *target.name
			*target.name = name
			if target.aliases != nil && len(*target.aliases) < interfaces.DRAFT_ALIASES_MAX &&
				utf8.RuneCountInString(old) <= maxAliasRunes && !strings.ContainsAny(old, ",\uff0c") &&
				!slices.Contains(*target.aliases, old) {
				*target.aliases = append(*target.aliases, old)
			}
			if target.aliases != nil {
				*target.aliases = withoutName(*target.aliases, name)
			}
		}
		if description := strings.TrimSpace(out.Description); target.redescribes && description != "" &&
			!inOtherLanguage(language, description) && utf8.RuneCountInString(description) <= interfaces.DRAFT_DESCRIPTION_MAX_LENGTH {
			*target.description = description
		}
	}
	// BKN refuses an alias that is another object type's name, or another property's.
	for _, ot := range items.ObjectTypes {
		ot.Aliases = slices.DeleteFunc(ot.Aliases, func(alias string) bool {
			return slices.ContainsFunc(items.ObjectTypes, func(other *interfaces.DraftObjectType) bool {
				return other != ot && other.Name == alias
			})
		})
		if len(ot.Aliases) == 0 {
			ot.Aliases = nil
		}
		dropAliasesOfOtherProperties(ot.Properties)
	}
}
