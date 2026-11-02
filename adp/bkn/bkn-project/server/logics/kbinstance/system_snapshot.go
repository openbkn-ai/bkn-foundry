// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

type SystemSnapshot struct {
	Targets []LinkTarget
	Records []ReconcileRecord
	Digest  string
}

var resourceIdentity = regexp.MustCompile(`^[a-zA-Z0-9_.:-]{1,128}$`)

// ReadSystemSnapshot reads the entire target query as the current operator,
// rather than treating Vega's 1,000-row single page as a complete source. A
// confirmed alternate business number matches without replacing original keys.
func ReadSystemSnapshot(ctx context.Context, access interfaces.VegaAccess, operator interfaces.AccountInfo, target *interfaces.DraftObjectType, identity string, properties []string, guard func(context.Context) error) (*SystemSnapshot, error) {
	reader, available := access.(interfaces.VegaSnapshotAccess)
	if !available || access == nil || guard == nil || operator.Type != "user" || operator.ID == "" || target == nil || !target.Adopted || target.Document != nil || target.NeedsIdentity || len(target.PrimaryKeys) != 1 || !resourceIdentity.MatchString(target.Table.ResourceID) {
		return nil, ErrInvalidBuild
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	tables, err := access.GetTables(ctx, operator, []string{target.Table.ResourceID})
	if err != nil {
		return nil, err
	}
	if len(tables) != 1 || tables[0] == nil || tables[0].ID != target.Table.ResourceID {
		return nil, ErrBuildChanged
	}
	fields := map[string]*interfaces.DraftProperty{}
	for _, field := range target.Properties {
		if field != nil {
			fields[field.Name] = field
		}
	}
	names := append(slices.Clone(properties), identity, target.PrimaryKeys[0])
	slices.Sort(names)
	names = slices.Compact(names)
	columns := []string{}
	for i, name := range names {
		field := fields[name]
		if field == nil || field.MappedField == "" || strings.ContainsRune(field.MappedField, 0) {
			return nil, ErrBuildChanged
		}
		found := false
		for _, column := range tables[0].Columns {
			if column.Name != field.MappedField {
				continue
			}
			typ := column.Type
			if typ == "large_integer" {
				typ = "integer"
			}
			found = typ == field.Type
			break
		}
		if !found {
			return nil, ErrBuildChanged
		}
		columns = append(columns, fmt.Sprintf("`%s` AS bkn_f_%d", strings.ReplaceAll(field.MappedField, "`", "``"), i))
	}
	for _, name := range []string{identity, target.PrimaryKeys[0]} {
		field := fields[name]
		if field.Type != "string" && field.Type != "text" && field.Type != "integer" && field.Type != "unsigned integer" {
			return nil, ErrInvalidBuild
		}
	}
	sql := "SELECT " + strings.Join(columns, ", ") + " FROM {{" + target.Table.ResourceID + "}} ORDER BY `" + strings.ReplaceAll(fields[target.PrimaryKeys[0]].MappedField, "`", "``") + "`"
	rows, err := reader.QuerySnapshot(ctx, operator, sql, 60, 100000, guard)
	if err != nil {
		return nil, err
	}
	snapshot := &SystemSnapshot{Targets: []LinkTarget{}, Records: []ReconcileRecord{}}
	keyed := map[string]bool{}
	records := map[string]bool{}
	for _, row := range rows {
		values := map[string]any{}
		for i, name := range names {
			value, exists := row[fmt.Sprintf("bkn_f_%d", i)]
			if !exists {
				return nil, ErrBuildChanged
			}
			// Native SQL drivers may serialize typed decimals/integers as strings.
			// Only declared numeric fields are converted, without float64 rounding.
			if text, ok := value.(string); ok {
				switch fields[name].Type {
				case "integer", "unsigned integer", "decimal", "float":
					number := json.Number(text)
					if !validInstanceValue(fields[name].Type, number) {
						return nil, ErrBuildChanged
					}
					value = number
				}
			}
			values[name] = value
		}
		business, record := values[identity], values[target.PrimaryKeys[0]]
		if !validInstanceValue(fields[identity].Type, business) || !validInstanceValue(fields[target.PrimaryKeys[0]].Type, record) {
			return nil, ErrBuildChanged
		}
		key, err := businessJoinKey(business)
		if err != nil || keyed[key] {
			return nil, ErrBuildChanged
		}
		recordKey, err := businessJoinKey(record)
		if err != nil || records[recordKey] {
			return nil, ErrBuildChanged
		}
		keyed[key], records[recordKey] = true, true
		snapshot.Targets = append(snapshot.Targets, LinkTarget{Identity: business, RecordID: record})
		snapshot.Records = append(snapshot.Records, ReconcileRecord{Identity: business, Values: values})
	}
	slices.SortFunc(snapshot.Records, func(a, b ReconcileRecord) int {
		left, _ := businessJoinKey(a.Identity)
		right, _ := businessJoinKey(b.Identity)
		return strings.Compare(left, right)
	})
	slices.SortFunc(snapshot.Targets, func(a, b LinkTarget) int {
		left, _ := businessJoinKey(a.Identity)
		right, _ := businessJoinKey(b.Identity)
		return strings.Compare(left, right)
	})
	raw, err := canonicalBusinessValue(struct {
		Resource, Identity string
		Records            []ReconcileRecord
	}{target.Table.ResourceID, identity, snapshot.Records})
	if err != nil {
		return nil, ErrInvalidBuild
	}
	digest := sha256.Sum256(raw)
	snapshot.Digest = hex.EncodeToString(digest[:])
	if err = guard(ctx); err != nil {
		return nil, err
	}
	return snapshot, nil
}
