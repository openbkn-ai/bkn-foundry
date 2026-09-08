# Issue 1342: ontology-query property-access exit inventory

This inventory records every ontology-query exit that can read or derive object
property values. The invariant is: resolve one request-scoped property plan,
require `full` for every operation/dependency input, and project before values
cross a response or observability boundary.

| Exit | Read/dependency gate | Response boundary | Automated coverage |
| --- | --- | --- | --- |
| Object query through Vega | `object_type.buildPropertyAccessPlan` | `propertyAccessPlan.projectRow` | `TestVegaAndOpenSearchUseSamePropertyProjection` |
| Object query through OpenSearch | Same plan; search/sort inputs require `full` | Same projection; `_score` requires an explicit full-property scoring condition | `TestScoreIsOnlyReturnedForExplicitFullPropertySearch` |
| Object schema and sample/preview data | Same plan filters metadata and projected rows | `none` is omitted; masked values are transformed before response | Existing property-plan schema and sample-data tests |
| Relation query by source/path/multi-hop | `requireFullPathInputs` checks direct, indirect, filtered-cross-join, filter, and sort dependencies | `objectInfoFromLevelObject` consumes only projected rows and safe system fields | `TestRelationMappingRequiresFullPropertiesOnBothSides`, `TestSubgraphProjectionUsesOnlySanitizedSystemFields` |
| Relation query by input objects | `requireFullRelationInputs` checks every discovered authorized relation before matching | Same subgraph projection | Same relation/property projection tests plus existing by-objects tests |
| Metric query and dry-run | `requireFullMetricInputs` covers definition/request filters, aggregation, group, sort, drill-down, and time inputs | Metric output contains only aggregate labels/values after the gate | `TestMetricRejectsMaskedAggregationInputBeforeDataRead`, `TestMetricRequiresFullFilterGroupSortAndTimeInputs` |
| Metric-backed LogicProperty | Object plan requires all property-sourced parameters to be `full`; metric execution applies the metric gate and existing metric proxy permission | Only the calculated LogicProperty value is added to the projected row | Existing LogicProperty dependency and proxy tests plus metric gate tests |
| Tool-backed LogicProperty | Object plan requires all property-sourced parameters to be `full`; existing LogicProperty-to-ToolBox proxy binding enforces execute permission | Only the selected tool result is added to the projected row | Existing `TestToolLogicPropertyUsesPublishedProxyBinding` and property-plan dependency tests |
| Action recall/preview | Action conditions and data-property-sourced parameters require `full`; logical parameters retain their own dependency/proxy gates | Only action parameters and safely projected identity are assembled | Object-plan required-dependency tests plus existing action recall tests |
| `_display` | Display property must be `masked` or `full` | Derived only from the sanitized property map; subgraphs reuse projected `_display` | `TestPropertyAccessPlanProjectsAndMasksBeforeResponseAssembly`, `TestSubgraphProjectionUsesOnlySanitizedSystemFields` |
| `_instance_identity` / `_instance_id` | Every primary-key property must be `full` | Derived in object projection; subgraphs never concatenate raw or masked keys | `TestPropertyAccessPlanDoesNotFetchPartialPrimaryKeyForSystemFields`, `TestSubgraphProjectionUsesOnlySanitizedSystemFields` |
| `_score` | Explicit scoring condition and all referenced fields must be `full` | Omitted for non-scoring/default queries | `TestScoreIsOnlyReturnedForExplicitFullPropertySearch` |
| Cursor / pagination state | Cursor is authenticated and permissions are re-resolved per page | Raw `search_after` is rejected and never serialized by object or subgraph responses/evidence shapes | `TestObjectQueryCursorReauthorizesAndNeverExposesRawPosition`, `TestSubgraphResponseExposesOpaqueCursorOnly`, `TestEvidenceQueryShapeDoesNotContainRawPaginationValues` |
| Logs, traces, errors, evidence | Code logs identifiers/counts/error metadata, not result rows; evidence hashes request shapes | No raw object row or raw page-state payload is emitted | Watermark/evidence test above and code review of the listed exits |
| Cache / async replay | ontology-query has no post-read raw-row cache or async replay store on these paths | Not applicable; all query results remain request-local | Inventory/code review |

Internal relation backing-resource rows are request-local join inputs. They are
protected by the existing relation-type managed proxy, are never returned, and
are logged only by row count.
