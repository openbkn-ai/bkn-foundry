// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"cmp"
	"encoding/json"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

type InstanceCitation struct {
	BlockID string `json:"block_id"`
	Quote   string `json:"quote"`
}
type InstanceValue struct {
	Value    any                `json:"value"`
	Evidence []InstanceCitation `json:"evidence"`
}
type InstanceRecord struct {
	Reference    string                   `json:"reference"`
	ObjectTypeID string                   `json:"object_type_id"`
	Anchor       InstanceCitation         `json:"anchor"`
	Values       map[string]InstanceValue `json:"values"`
}
type InstanceRelation struct {
	RelationTypeID string             `json:"relation_type_id"`
	Source         string             `json:"source"`
	Target         string             `json:"target"`
	Evidence       []InstanceCitation `json:"evidence"`
}
type FileExtraction struct {
	Records   []InstanceRecord   `json:"records"`
	Relations []InstanceRelation `json:"relations"`
}
type locatedEvidence struct {
	BlockID string `json:"block_id"`
	Start   int    `json:"source_start"`
	End     int    `json:"source_end"`
	Quote   string `json:"source_quote"`
}

func parseFileExtraction(output string) (*FileExtraction, error) {
	if len(output) > 8<<20 {
		return nil, ErrInvalidBuild
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var result FileExtraction
	if decoder.Decode(&result) != nil || result.Records == nil || result.Relations == nil || len(result.Records) > 10000 || len(result.Relations) > 20000 {
		return nil, ErrInvalidBuild
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, ErrInvalidBuild
	}
	return &result, nil
}
func locateInstance(input *kb.FileInput, quote InstanceCitation, includeOriginal bool) (locatedEvidence, error) {
	text, found := input.Text[quote.BlockID]
	if !found || quote.Quote == "" || !utf8.ValidString(quote.Quote) || utf8.RuneCountInString(quote.Quote) > 4000 {
		return locatedEvidence{}, ErrInvalidBuild
	}
	i := strings.Index(text, quote.Quote)
	if i < 0 || strings.Contains(text[i+1:], quote.Quote) {
		return locatedEvidence{}, ErrInvalidBuild
	}
	for _, block := range input.Blocks {
		if block.BlockID == quote.BlockID {
			start := block.Start + utf8.RuneCountInString(text[:i])
			q := ""
			if includeOriginal {
				q = quote.Quote
			}
			return locatedEvidence{BlockID: quote.BlockID, Start: start, End: start + utf8.RuneCountInString(quote.Quote), Quote: q}, nil
		}
	}
	return locatedEvidence{}, ErrInvalidBuild
}
func locateInstances(input *kb.FileInput, quotes []InstanceCitation, includeOriginal bool) ([]locatedEvidence, error) {
	if len(quotes) == 0 || len(quotes) > 20 {
		return nil, ErrInvalidBuild
	}
	result := []locatedEvidence{}
	seen := map[string]bool{}
	for _, quote := range quotes {
		q, err := locateInstance(input, quote, includeOriginal)
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(q)
		if !seen[string(raw)] {
			seen[string(raw)] = true
			result = append(result, q)
		}
	}
	slices.SortFunc(result, func(a, b locatedEvidence) int {
		if n := strings.Compare(a.BlockID, b.BlockID); n != 0 {
			return n
		}
		if n := cmp.Compare(a.Start, b.Start); n != 0 {
			return n
		}
		return cmp.Compare(a.End, b.End)
	})
	return result, nil
}
func validInstanceValue(typ string, value any) bool {
	if value == nil {
		return true
	}
	switch typ {
	case "string", "text":
		v, ok := value.(string)
		return ok && utf8.ValidString(v) && len(v) <= 1<<20
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer", "unsigned integer":
		n, ok := value.(json.Number)
		if !ok {
			return false
		}
		if typ == "unsigned integer" {
			_, err := strconv.ParseUint(n.String(), 10, 64)
			return err == nil
		}
		_, err := n.Int64()
		return err == nil
	case "float", "decimal":
		n, ok := value.(json.Number)
		if !ok {
			return false
		}
		f, err := n.Float64()
		return err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	case "date", "time", "datetime", "timestamp":
		v, ok := value.(string)
		if !ok {
			return false
		}
		layouts := []string{time.RFC3339Nano}
		if typ == "date" {
			layouts = []string{"2006-01-02"}
		}
		if typ == "time" {
			layouts = []string{"15:04:05.999999999"}
		}
		for _, layout := range layouts {
			if _, err := time.Parse(layout, v); err == nil {
				return true
			}
		}
		return false
	case "json":
		raw, err := json.Marshal(value)
		return err == nil && len(raw) <= 1<<20
	}
	return false
}
func instanceObject(items interfaces.DraftItems, id string) *interfaces.DraftObjectType {
	for _, o := range items.ObjectTypes {
		if o != nil && o.ID == id && o.Adopted && o.Document != nil && !o.NeedsIdentity {
			return o
		}
	}
	return nil
}
func instanceProperty(object *interfaces.DraftObjectType, name string) *interfaces.DraftProperty {
	for _, p := range object.Properties {
		if p != nil && p.Name == name {
			return p
		}
	}
	return nil
}
func ordinaryInstanceProperty(object *interfaces.DraftObjectType, p *interfaces.DraftProperty) bool {
	return p != nil && (!p.ReadOnly || ((object.Document.Role == "fact" || object.Document.Role == "event") && (p.Name == "effective_from" || p.Name == "effective_to")))
}
func businessKey(object *interfaces.DraftObjectType, record InstanceRecord) (string, error) {
	if object.Document.Role != "entity" || len(object.PrimaryKeys) == 0 {
		return "", ErrInvalidBuild
	}
	key := map[string]any{}
	for _, name := range object.PrimaryKeys {
		value, exists := record.Values[name]
		if !exists || value.Value == nil {
			return "", ErrInvalidBuild
		}
		if s, ok := value.Value.(string); ok && strings.TrimSpace(s) == "" {
			return "", ErrInvalidBuild
		}
		key[name] = value.Value
	}
	raw, err := canonicalBusinessValue(key)
	if err != nil {
		return "", ErrInvalidBuild
	}
	return string(raw), nil
}

func extractionVersion(task *interfaces.BknAgentTask) (string, error) {
	if task == nil || task.AgentID != kb.DocumentExtractionAgentID || task.Status != interfaces.BKN_AGENT_TASK_SUCCEEDED || task.AgentVersion == nil || *task.AgentVersion < 1 || task.PromptVersion == nil || *task.PromptVersion < 1 || task.ModelID == "" {
		return "", ErrInvalidBuild
	}
	raw, err := json.Marshal(struct {
		Agent     int    `json:"agent"`
		Prompt    *int   `json:"prompt"`
		Model     string `json:"model"`
		ModelName string `json:"model_name"`
	}{*task.AgentVersion, task.PromptVersion, task.ModelID, task.Model})
	return string(raw), err
}
