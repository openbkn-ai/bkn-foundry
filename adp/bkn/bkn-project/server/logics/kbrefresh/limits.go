// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"fmt"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"math"
	"slices"
)

type Limits struct {
	MaxChangedPercent int   `json:"max_changed_percent"`
	MaxRemovedPercent int   `json:"max_removed_percent"`
	MaxDurationMillis int64 `json:"max_duration_millis"`
}

func DefaultLimits() Limits {
	return Limits{MaxChangedPercent: 20, MaxRemovedPercent: 10, MaxDurationMillis: 10 * 60 * 1000}
}

// ValidateLimits reports settings EvaluateLimits would refuse, for the startup log.
func ValidateLimits(limits Limits) error {
	if limits.MaxChangedPercent < 0 || limits.MaxChangedPercent > 100 || limits.MaxRemovedPercent < 0 || limits.MaxRemovedPercent > 100 || limits.MaxDurationMillis <= 0 {
		return fmt.Errorf("percentages must be within 0-100 and the duration positive, got %d%%, %d%%, %d ms", limits.MaxChangedPercent, limits.MaxRemovedPercent, limits.MaxDurationMillis)
	}
	return nil
}
func (s *Service) limits() Limits {
	if s.Limits != nil {
		return *s.Limits
	}
	return DefaultLimits()
}

// EvaluateLimits uses actual completed-task durations from this fixed execution
// model, normalized by available source chunk counts. An unknown estimate stays
// unknown; neither model self-scores nor invented processing speeds fill it in.
func EvaluateLimits(limits Limits, previous *interfaces.KBBuild, plan *ManifestPlan) (*interfaces.KBRefreshEstimate, error) {
	if previous == nil || plan == nil || plan.Build == nil || ValidateLimits(limits) != nil {
		return nil, ErrInvalidManifest
	}
	result := &interfaces.KBRefreshEstimate{PreviousFiles: len(previous.Files), ChangedFiles: len(plan.Changed), Reasons: []string{}}
	current := map[string]bool{}
	for _, step := range plan.Build.Files {
		current[step.File.Key()] = true
	}
	removed := map[string]bool{}
	for _, file := range plan.Removed {
		if !current[file.Key()] {
			removed[file.Key()] = true
		}
	}
	result.RemovedFiles = len(removed)
	denominator := max(1, len(previous.Files))
	if result.ChangedFiles*100 > denominator*limits.MaxChangedPercent {
		result.Reasons = append(result.Reasons, "changes_large")
	}
	if result.RemovedFiles*100 > denominator*limits.MaxRemovedPercent {
		result.Reasons = append(result.Reasons, "removals_large")
	}
	if len(plan.Changed) == 0 {
		duration := int64(0)
		result.DurationMillis = &duration
		return result, nil
	}
	rates := []int64{}
	for _, step := range previous.Files {
		if step.DurationMillis == nil || *step.DurationMillis < 0 || step.File.ChunkCount <= 0 {
			continue
		}
		duration := *step.DurationMillis
		rate := duration / step.File.ChunkCount
		if duration%step.File.ChunkCount != 0 {
			rate++
		}
		rates = append(rates, max(int64(1), rate))
	}
	if len(rates) == 0 {
		result.Reasons = append(result.Reasons, "duration_unknown")
		return result, nil
	}
	slices.Sort(rates)
	// Use the upper median for a conservative even-sized observation set.
	rate := rates[len(rates)/2]
	duration := int64(0)
	for _, index := range plan.Changed {
		if index < 0 || index >= len(plan.Build.Files) {
			return nil, ErrInvalidManifest
		}
		chunks := plan.Build.Files[index].File.ChunkCount
		if chunks <= 0 {
			result.Reasons = append(result.Reasons, "duration_unknown")
			return result, nil
		}
		if chunks > (math.MaxInt64-duration)/rate {
			duration = math.MaxInt64
			break
		}
		duration += chunks * rate
	}
	result.DurationMillis = &duration
	if duration > limits.MaxDurationMillis {
		result.Reasons = append(result.Reasons, "duration_large")
	}
	return result, nil
}
