// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"slices"
)

// FusionClaim is a validated, independently evidenced assertion read from a
// Dataset. Unknown priority is nil, never an invented zero-strength source.
type FusionClaim struct {
	ID, EntityID, Property     string
	Value                      any
	EffectiveFrom, EffectiveTo *int64
	Strength                   *int
	RecordedAt                 int64
}
type FusionResult struct {
	Value                        any
	Current, Superseded, Pending []string
	Reason                       string
}

func (result *FusionResult) sort() {
	slices.Sort(result.Current)
	slices.Sort(result.Superseded)
	slices.Sort(result.Pending)
}

// ResolvePropertyClaims never uses a filename, row identifier or model order
// to break a semantic tie. Nil means not mentioned, not an instruction to erase.
// A pending result is a review proposal; callers must not overwrite a previously
// evidenced current value with it. Removed evidence is handled independently.
func ResolvePropertyClaims(claims []FusionClaim, now int64, temporal bool) (FusionResult, error) {
	result := FusionResult{Current: []string{}, Superseded: []string{}, Pending: []string{}}
	if now <= 0 {
		return result, ErrInvalidBuild
	}
	if len(claims) == 0 {
		return result, nil
	}
	seen := map[string]bool{}
	active := []FusionClaim{}
	values := map[string]string{}
	entity, property := claims[0].EntityID, claims[0].Property
	if entity == "" || property == "" {
		return result, ErrInvalidBuild
	}
	missingTime := false
	for _, claim := range claims {
		if claim.ID == "" || seen[claim.ID] || claim.EntityID != entity || claim.Property != property {
			return FusionResult{}, ErrInvalidBuild
		}
		seen[claim.ID] = true
		if claim.Value == nil {
			continue
		}
		encoded, err := canonicalBusinessValue(claim.Value)
		if err != nil {
			return FusionResult{}, err
		}
		values[claim.ID] = string(encoded)
		if claim.EffectiveFrom != nil && claim.EffectiveTo != nil && *claim.EffectiveTo <= *claim.EffectiveFrom {
			return FusionResult{}, ErrInvalidBuild
		}
		if claim.EffectiveFrom != nil && *claim.EffectiveFrom > now || claim.EffectiveTo != nil && *claim.EffectiveTo <= now {
			result.Pending = append(result.Pending, claim.ID)
			continue
		}
		if claim.EffectiveFrom == nil {
			missingTime = true
		}
		active = append(active, claim)
	}
	pending := func(reason string) FusionResult {
		for _, claim := range active {
			result.Pending = append(result.Pending, claim.ID)
		}
		result.Reason = reason
		result.sort()
		return result
	}
	if len(active) == 0 {
		result.Reason = "outside_effective_period"
		result.sort()
		return result, nil
	}
	sameValues := func(group []FusionClaim) bool {
		for i := 1; i < len(group); i++ {
			if values[group[i].ID] != values[group[0].ID] {
				return false
			}
		}
		return true
	}
	if temporal && missingTime {
		return pending("effective_time_required"), nil
	}
	top := slices.Clone(active)
	if missingTime {
		if !sameValues(top) {
			return pending("effective_time_required"), nil
		}
	} else {
		latest := *top[0].EffectiveFrom
		for _, claim := range top {
			latest = max(latest, *claim.EffectiveFrom)
		}
		top = slices.DeleteFunc(top, func(claim FusionClaim) bool { return *claim.EffectiveFrom != latest })
	}
	if !sameValues(top) {
		strength := 0
		for i, claim := range top {
			if claim.Strength == nil {
				return pending("evidence_strength_required"), nil
			}
			if i == 0 || *claim.Strength > strength {
				strength = *claim.Strength
			}
		}
		top = slices.DeleteFunc(top, func(claim FusionClaim) bool { return *claim.Strength != strength })
	}
	if !sameValues(top) {
		latest := int64(0)
		for _, claim := range top {
			if claim.RecordedAt <= 0 {
				return pending("recorded_time_required"), nil
			}
			latest = max(latest, claim.RecordedAt)
		}
		top = slices.DeleteFunc(top, func(claim FusionClaim) bool { return claim.RecordedAt != latest })
	}
	if !sameValues(top) {
		return pending("conflicting_evidence"), nil
	}
	selected := map[string]bool{}
	for _, claim := range top {
		result.Current = append(result.Current, claim.ID)
		selected[claim.ID] = true
	}
	for _, claim := range active {
		if !selected[claim.ID] {
			result.Superseded = append(result.Superseded, claim.ID)
		}
	}
	result.Value = top[0].Value
	result.sort()
	return result, nil
}
