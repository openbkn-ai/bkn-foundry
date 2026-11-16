# Administrator permission projection

`POST /api/safe/v1/admin/permission-projection` requires authenticated
`admin-authz:view`. It projects effective operations for a visible page of
knowledge network resources. It does not write policies or change the existing
object-grants list contract.

```json
{
  "network_id": "kn-1",
  "subject": {"type": "user", "id": "user-1"},
  "resources": [{"type": "object_type", "id": "kn-1/orders"}]
}
```

Subjects may be active users or existing roles. Candidates must be concrete
knowledge_network, object_type, relation_type, action_type or metric resources
within the named network. Domain services remain responsible for enumerating
resources and pagination; send only the current page (maximum 100 candidates).
No platform-wide policy scan or per-resource permission check is performed:
the existing sparse batch evaluator resolves the subject's grant index once.

The response has `entries` with `accessor_id`, `accessor_type`, `resource`,
allowed `operations` and server-computed `effective_decisions`, including deny,
basis and prerequisite information. Role inheritance, wildcard and parent
resource decisions use the existing effective evaluation semantics. This is
not a full provenance trace by default. Send `include_sources: true` with exactly
one resource to retrieve the existing engine's `steps` and `requirements` for
each operation. Matched rules identify the subject ID and kind, source rule ID,
resource and effect, so a client can distinguish a user's direct grant from
role or parent-resource access. This detail mode reuses `ExplainOperation`;
the normal page mode never performs those per-operation explanation walks.

Creation (`create`) is excluded: this endpoint represents existing resource
instances, while creation is a resource-type capability. Existing policy and
creation-check contracts are unchanged.

An entitled EE assembly adds `configuration_summary` to object-type entries:
`row_policies`, `row_conditions`, `field_rules`, `masked_rules`, `hidden_rules`.
These count active direct and role-inherited configuration records, not the
final number of visible or restricted fields. Complete row predicates and
effective field levels remain on their detail endpoints. EE owns both stores;
Core never reads EE tables. The whole page uses two configuration queries.
Unavailable capabilities omit summaries rather than returning false zeros.

Source detail responses include `subjects` (ID to display name) and
`source_targets` (grant ID to subject/resource management target). Targets have
`kind` (`object_grants`, `role_permissions`, `read_only`) and `can_manage` for
the authenticated caller. Built-in role type-wide rules are read-only. This
indicates whether the caller can open that subject/resource management surface,
not whether this particular source record is revocable; write endpoints retain
their source ownership and permission checks.

Deploy bkn-safe before the Studio client using this endpoint. Old deployments
return 404; Studio reports failure rather than substituting direct grants for
effective permissions. A whole-page request fails if evaluation fails.

`GET /api/safe/v1/admin/permission-projection/fields` accepts `accessor_id`,
`accessor_type` (`user` or `role`) and `object_type_ref` (`network/object`). It
requires `admin-authz:view` and returns every published data field in `decisions`
with `name`, `display_name`, effective `level` and `source`. Explicit and
inherited field rules use the runtime resolver and remain bounded by object
access. Missing explicit configuration therefore does not produce an empty
field list. Schema or resolver failures return errors rather than successful
empty results. The existing sparse property-grants editing contract is unchanged.

Source explanation queries are restricted to the selected subjects and the
current resource's exact or wildcard candidate records. One request reuses
these records across operations; unrelated concrete-resource policies are not
loaded. Row and field details are loaded independently on demand by Studio.
