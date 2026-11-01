// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbresource

import (
	"math"
	"sort"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// chooseSamples copies metadata; it cannot change versions or read held-out text.
func chooseSamples(files []interfaces.KBResourceFile, plan interfaces.KBSamplePlan, shuffle func(int, func(int, int))) ([]interfaces.KBResourceFile, error) {
	selected := append([]interfaces.KBResourceFile{}, files...)
	eligible := map[string]int{}
	for i, file := range selected {
		selected[i].SampleRole = ""
		if _, duplicate := eligible[file.Key()]; duplicate {
			return nil, interfaces.ErrKBSamplePlan
		}
		if file.ContentVersionID != "" {
			eligible[file.Key()] = i
		}
	}
	assign := func(ids []string, role string) bool {
		for _, id := range ids {
			i, found := eligible[id]
			if !found || selected[i].SampleRole != "" {
				return false
			}
			selected[i].SampleRole = role
		}
		return true
	}
	switch plan.Mode {
	case "specified":
		if plan.Ratio != nil || plan.Minimum != nil || plan.AcceptanceRatio != nil || plan.AcceptanceMinimum != nil {
			return nil, interfaces.ErrKBSamplePlan
		}
		if !assign(plan.DebugIDs, "debug") || !assign(plan.AcceptanceIDs, "acceptance") {
			return nil, interfaces.ErrKBSamplePlan
		}
	case "random":
		if len(plan.DebugIDs) > 0 || len(plan.AcceptanceIDs) > 0 {
			return nil, interfaces.ErrKBSamplePlan
		}
		debug, ok := sampleCount(len(eligible), plan.Ratio, plan.Minimum, false)
		if !ok {
			return nil, interfaces.ErrKBSamplePlan
		}
		accept, ok := sampleCount(len(eligible), plan.AcceptanceRatio, plan.AcceptanceMinimum, true)
		if !ok || debug+accept > len(eligible) {
			return nil, interfaces.ErrKBSamplePlan
		}
		stratified(selected, debug, "debug", shuffle)
		stratified(selected, accept, "acceptance", shuffle)
	default:
		return nil, interfaces.ErrKBSamplePlan
	}
	return selected, nil
}

func sampleCount(total int, ratio *float64, minimum *int, optional bool) (int, bool) {
	if optional && ratio == nil && minimum == nil {
		return 0, true
	}
	if ratio == nil || minimum == nil || math.IsNaN(*ratio) || math.IsInf(*ratio, 0) || *ratio < 0 || *ratio > 1 || *minimum < 0 {
		return 0, false
	}
	count := max(int(math.Ceil(float64(total)*(*ratio))), *minimum)
	return count, count <= total
}

// Largest-remainder allocation keeps the requested total exact. Randomized ties
// and in-stratum shuffles avoid favoring a directory's name or physical order.
func stratified(files []interfaces.KBResourceFile, count int, role string, shuffle func(int, func(int, int))) {
	if count == 0 {
		return
	}
	groups := map[string][]int{}
	total := 0
	for i, file := range files {
		if file.ContentVersionID != "" && file.SampleRole == "" {
			key := file.KnowledgeBaseID + "\x00" + file.CatalogID
			groups[key] = append(groups[key], i)
			total++
		}
	}
	type allocation struct {
		key       string
		count     int
		remainder int
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
	allocations := make([]allocation, 0, len(keys))
	assigned := 0
	for _, key := range keys {
		product := count * len(groups[key])
		n := product / total
		assigned += n
		allocations = append(allocations, allocation{key, n, product % total})
	}
	sort.SliceStable(allocations, func(i, j int) bool { return allocations[i].remainder > allocations[j].remainder })
	for i := 0; i < count-assigned; i++ {
		allocations[i].count++
	}
	for _, allocation := range allocations {
		indices := groups[allocation.key]
		shuffle(len(indices), func(i, j int) { indices[i], indices[j] = indices[j], indices[i] })
		for _, index := range indices[:allocation.count] {
			files[index].SampleRole = role
		}
	}
}
