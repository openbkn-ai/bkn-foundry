// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

type discoverTaskReconcileProgress struct {
	metadataTotal     int
	metadataProcessed int
	lastProgress      int

	sourceListed        bool
	resourcesReconciled bool
	metadataEnriched    bool
}

func (progress *discoverTaskReconcileProgress) MarkSourceListed() (int, bool) {
	progress.sourceListed = true
	return progress.changed()
}

func (progress *discoverTaskReconcileProgress) SetMetadataTotal(total int) {
	progress.metadataTotal = total
}

func (progress *discoverTaskReconcileProgress) AdvanceMetadata() (int, bool) {
	if progress.metadataTotal == 0 {
		return progress.changed()
	}
	progress.metadataProcessed++
	return progress.changed()
}

func (progress *discoverTaskReconcileProgress) MarkResourcesReconciled() (int, bool) {
	progress.resourcesReconciled = true
	return progress.changed()
}

func (progress *discoverTaskReconcileProgress) MarkMetadataEnriched() (int, bool) {
	progress.metadataProcessed = progress.metadataTotal
	progress.metadataEnriched = true
	return progress.changed()
}

func (progress *discoverTaskReconcileProgress) calculate() int {
	if progress.metadataEnriched {
		return 95
	}
	if progress.resourcesReconciled {
		if progress.metadataTotal > 0 {
			return 20 + progress.metadataProcessed*75/progress.metadataTotal
		}
		return 20
	}
	if progress.sourceListed {
		return 5
	}
	return 0
}

func (progress *discoverTaskReconcileProgress) changed() (int, bool) {
	next := progress.calculate()
	if next <= progress.lastProgress {
		return next, false
	}
	progress.lastProgress = next
	return next, true
}
