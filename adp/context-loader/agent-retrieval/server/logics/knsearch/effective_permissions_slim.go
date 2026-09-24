// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knsearch (response slimming for the model surface)
// file: effective_permissions_slim.go
package knsearch

import (
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// slimObjectTypesForModel converges an object type list to what the model surface publishes,
// once the retrieval that needed the full shape is done.
//
// It runs where the object types are still typed. Both search_schema and search_instance hand
// their results on through a JSON round trip (toAnySlice), after which every object type is a
// map[string]any and rules like these can no longer be applied by field.
func slimObjectTypesForModel(objectTypes []*interfaces.KnSearchObjectType, modelSurface bool) {
	trimToIndexBackedOperations(objectTypes, modelSurface)
	omitUnrestrictedPermissions(objectTypes, modelSurface)
}

// omitUnrestrictedPermissions drops an object type's effective_permissions when every property
// in it is full.
//
// The map names every property of the object type a second time, so on a network the caller may
// read in full it repeats "full" for each one and says nothing. Measured on the test deployment,
// a supply-chain sample answered one search_schema with 124 entries, all full: 3.3KB, about 15%
// of its object_types, carried again in every later model turn. A map with any masked or schema
// entry is the one that tells the caller something and is kept whole.
//
// get_kn_detail has done this since the progressive disclosure work; search_schema and
// search_instance carry a different shape and never got it.
func omitUnrestrictedPermissions(objectTypes []*interfaces.KnSearchObjectType, modelSurface bool) {
	if !modelSurface {
		return
	}
	for _, objType := range objectTypes {
		if objType == nil || len(objType.EffectivePermissions) == 0 {
			continue
		}
		restricted := false
		for _, level := range objType.EffectivePermissions {
			if level != interfaces.PropertyAccessFull {
				restricted = true
				break
			}
		}
		if !restricted {
			objType.EffectivePermissions = nil
		}
	}
}
