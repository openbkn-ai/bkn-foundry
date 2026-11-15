// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

// The stable codes of the declaration rules (SPECIFICATION.md §4.3, §6.8, §7.6) a refusal reports in
// SpecError.Rule, with the Params each carries. A caller words the refusal from the code and the
// params; the English message stays for logs and the command line.
const (
	// Semantic and property rules. Property is set.
	RuleSemanticUnknown      = "semantic_unknown"       // semantic, allowed
	RuleSemanticTypeMismatch = "semantic_type_mismatch" // semantic, allowed, type
	RuleSemanticDuplicated   = "semantic_duplicated"    // semantic, other_property

	// Semantic rules of the whole definition.
	RuleEndTimeWithoutEventTime       = "end_time_without_event_time"
	RuleLatitudeLongitudeUnpaired     = "latitude_longitude_unpaired"
	RuleLocationWithLatitudeLongitude = "location_with_latitude_longitude"
	RuleCRSMismatch                   = "crs_mismatch" // latitude_crs, longitude_crs

	// Constraint rules. Property is set.
	RuleValueTypeWithValueRules = "value_type_with_value_rules" // value_type
	RuleUniqueTypeUnsupported   = "unique_type_unsupported"     // type
	RuleCRSWithoutGeoSemantic   = "crs_without_geo_semantic"
	RuleCRSInvalid              = "crs_invalid"            // crs, allowed
	RuleTimezoneTypeMismatch    = "timezone_type_mismatch" // type
	RuleTimezoneInvalid         = "timezone_invalid"       // timezone
	RuleRangeBoundsMissing      = "range_bounds_missing"
	RuleRangeNotNumeric         = "range_not_numeric"
	RuleRangeNotDate            = "range_not_date"             // type
	RuleRangeNotTime            = "range_not_time"             // type
	RuleRangeMinGreaterThanMax  = "range_min_greater_than_max" // min, max
	RuleRangeMinLaterThanMax    = "range_min_later_than_max"   // min, max
	RuleRangeTypeUnsupported    = "range_type_unsupported"     // type
	RuleLengthBoundsMissing     = "length_bounds_missing"
	RuleLengthBoundNegative     = "length_bound_negative"       // bound
	RuleLengthTypeMismatch      = "length_type_mismatch"        // type, allowed
	RuleLengthMinGreaterThanMax = "length_min_greater_than_max" // min, max
	RulePatternTypeMismatch     = "pattern_type_mismatch"       // type, allowed
	RulePatternInvalid          = "pattern_invalid"             // pattern, error
	RuleUnitTypeMismatch        = "unit_type_mismatch"          // type

	// Alias rules. Property is set for the alias of a data property.
	RuleAliasesTooMany      = "aliases_too_many" // count, max
	RuleAliasEmpty          = "alias_empty"
	RuleAliasFullWidthComma = "alias_full_width_comma" // alias
	RuleAliasContainsComma  = "alias_contains_comma"   // alias
	RuleAliasTooLong        = "alias_too_long"         // alias, max
	RuleAliasDuplicated     = "alias_duplicated"       // alias
	RuleAliasEqualsOwnName  = "alias_equals_own_name"  // alias
	RuleAliasConflict       = "alias_conflict"         // alias, conflicts_with or conflicts_with_property

	// Lifecycle rules.
	RuleLifecycleFieldsWithoutDeprecation = "lifecycle_fields_without_deprecation"
	RuleLifecycleReasonMissing            = "lifecycle_reason_missing"
	RuleLifecycleReplacedByInvalid        = "lifecycle_replaced_by_invalid" // replaced_by
	RuleLifecycleReplacedBySelf           = "lifecycle_replaced_by_self"    // replaced_by
	RuleLifecycleStatusInvalid            = "lifecycle_status_invalid"      // status, allowed
	RuleReplacedByMissing                 = "replaced_by_missing"           // replaced_by
	RuleReplacedByDeprecated              = "replaced_by_deprecated"        // replaced_by

	// Relation type rules.
	RuleReverseNameInvalid            = "reverse_name_invalid"        // reverse_name, reason
	RuleImportanceInvalid             = "importance_invalid"          // importance, allowed
	RuleVerificationInvalid           = "verification_invalid"        // verification, allowed
	RuleCardinalityInvalid            = "cardinality_invalid"         // cardinality, allowed
	RuleCardinalityCrossJoin          = "cardinality_cross_join"      // cardinality
	RuleCharacteristicsEndsDiffer     = "characteristics_ends_differ" // source, target
	RuleSymmetricCardinality          = "symmetric_cardinality"       // cardinality
	RuleSymmetricReverseName          = "symmetric_reverse_name"
	RuleSymmetricRequired             = "symmetric_required"
	RuleListValueInvalid              = "list_value_invalid"               // field, value, allowed
	RuleListValueDuplicated           = "list_value_duplicated"            // field, value
	RuleRelationPropertiesNotIndirect = "relation_properties_not_indirect" // endpoint_type
	RuleRelationPropertiesTooMany     = "relation_properties_too_many"     // count, max
	RuleRelationPropertyInvalid       = "relation_property_invalid"        // reason
	RuleRelationPropertyJoinsEnds     = "relation_property_joins_ends"     // mapped_field
	RuleRelationPropertyUnique        = "relation_property_unique"

	// Fallback path rules.
	RuleFallbackForInvalid         = "fallback_for_invalid"         // fallback_for
	RuleFallbackForNotSecondary    = "fallback_for_not_secondary"   // fallback_for
	RuleFallbackForSelf            = "fallback_for_self"            // fallback_for
	RuleFallbackCrossJoin          = "fallback_cross_join"          // relation_type
	RuleFallbackCharacteristics    = "fallback_characteristics"     // relation_type
	RuleFallbackTargetMissing      = "fallback_target_missing"      // fallback_for
	RuleFallbackTargetNotPrimary   = "fallback_target_not_primary"  // fallback_for
	RuleFallbackTargetDeprecated   = "fallback_target_deprecated"   // fallback_for
	RuleFallbackEndsDiffer         = "fallback_ends_differ"         // fallback_for, source, target, primary_source, primary_target
	RuleFallbackPrimaryCardinality = "fallback_primary_cardinality" // fallback_for, cardinality
	RuleFallbackAlreadyTaken       = "fallback_already_taken"       // fallback_for, other_fallback
	RuleEndpointDeprecated         = "endpoint_deprecated"          // object_type

	// Value type rules of the network.
	RuleValueTypeDuplicated     = "value_type_duplicated"      // value_type
	RuleValueTypeNameDuplicated = "value_type_name_duplicated" // value_type, other_value_type, name
	RuleValueTypeUndefined      = "value_type_undefined"       // value_type; property is set
	RuleValueTypeBaseMismatch   = "value_type_base_mismatch"   // value_type, base_type, type; property is set
)
