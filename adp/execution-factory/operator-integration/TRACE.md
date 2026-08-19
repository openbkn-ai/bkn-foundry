# operator-integration BKN Trace access specification.

> Status: 2.1 Action Causal Closed Loop Implementation Baseline.
> Update time: 2026-07-25.
>Based on: `bkn-docs/docs/foundry/bkn-trace/registry/Core Business Event Registry.md`

## 1. Module responsibilities.

- Module name: `action-execution`; Observation service: `operator-integration`.
- Running form: Go HTTP/MCP/toolbox/sandbox execution service.
- Code path: `adp/execution-factory/operator-integration`.
- Trace span uses `1.0.0`, Evidence event is written to `2.1.0` by default.
- This module only states permission decisions, execution attempts and execution results, and does not generate action suggestions or approval applications on behalf of the Agent.

## 2. Action factual responsibility.

| Facts | Responsible party | Behavior of this module |
| --- | --- | --- |
| `action.recommended` | Agent, AI application or workflow | Not generated; requires upstream to write first |
| `action.approval_requested` | Agent, AI application, or workflow | Not generated; accepts inbound event ID as direct cause |
| `action.approved` / `action.rejected` | The real permission boundaries of operator-integration | Generated after permission check |
| `action.executed` | operator-integration | Generated when execution actually starts or when an execution attempt fails |
| `action.result_recorded` | operator-integration | Record result hash and controlled task/artifact ref |

The upstream must reliably submit `recommended -> approval_requested` before calling the execution entry. execution does not guess suggestions based on tool parameters and does not make up for missing upstream state.

## 3. Entry and context.

Action execution reuses the permissions and execution boundaries of the real toolbox `ExecuteTool`. In addition to your existing identity card, you must bring:

```text
traceparent
bkn-request-id
bkn-interaction-id
bkn-operation-id
bkn-causation-event-id
bkn-claim-id
bkn-attempt
bkn-action-instance-id
bkn-action-type
bkn-action-reversible
bkn-action-policy-ref
bkn-action-observed-at
bkn-action-approval-requested-event-id
x-account-id
x-account-type
x-business-domain
```

- `bkn-action-approval-requested-event-id` is the direct cause of `approved/rejected`.
- `traceparent` must be the original valid W3C value and passed into the 2.1 ingest envelope unchanged.
- `x-business-domain` must be a real business domain. If it is missing, Action evidence will not be enabled and it is forbidden to replace it with account id.
- `bkn-operation-id` remains unchanged on retries, `bkn-attempt` is incremented and goes into event ID.
- The original execution behavior is maintained when any required causal field is missing, but no orphan Action event is created.
- The current automatic testing policy only accepts `action_type=monitor`, `reversible=true`, `policy_ref=e2e-monitor-auto-approve`.
- Other Actions must not be automatically approved, nor may they be misjudged as Actions when called by ordinary tools.

## 4. State machine and idempotence.

This module only continues the following states:

```text
approval_requested -> approved | rejected
approved -> executed
executed -> result_recorded
```

- `rejected` is the final state and no access to the toolbox database, metadata or execution agent is allowed after rejection.
- `approved -> executed(error) -> result_recorded(error)` is still recorded when the permission is passed but subsequent dependencies fail.
- event ID is stably derived from `action_instance_id + operation_id + attempt + event_type`.
- The content of the same event replayed in the same attempt must be exactly the same; event IDs must not be reused in different attempts.
- After the permission is passed and approved is successfully submitted, the execution right of `action_instance_id + attempt` must be obtained through Redis `SETNX` atomically before the actual side effects; failure to obtain it shall not be executed.
- After the execution results are written to the persistent gate, retry only returns the cached results and reissues the final state evidence without repeating side effects.
- Each stage uses the approval_requested time as the baseline to use a deterministic microsecond offset to ensure that the observed_at is different and the replay is stable.

## 5. Accurate payload.

| Events | Allowed fields |
| --- | --- |
| `action.approved` / `action.rejected` | `action_instance_id`、`actor_ref`、`policy_decision_ref`、`status` |
| `action.executed` | `action_instance_id`, `invocation_ref` or `tool_ref`, `status`, `error_category/error_hash` on error |
| `action.result_recorded` | `action_instance_id`, `result_hash`, `artifact_ref` or `task_ref`, `status` |

All hashes use `sha256:<64-bit lowercase hex>`. Actors, policy decisions, tools, and tasks all use irreversible controlled references; the original user ID, tool ID, or approval opinion must not be saved.

## 6. Trace and propagation.

| Call target | Protocol | Propagation fields | Constraints |
| --- | --- | --- | --- |
| toolbox/import tool | HTTP | trace, request, account, and controlled Action contexts | Do not propagate raw parameters and results |
| MCP server | MCP/HTTP | Trace metadata currently supported by `context.Context` | baggage only allowlist |
| sandbox control plane | HTTP | trace, request, account | Use existing execution timeout |
| authorization/bkn-safe | HTTP | trace, request, account | baggage does not put the original actor identification |

The allowed baggage is only `bkn.account.type`, `bkn.runtime.env`.

## 7. Sensitive data boundaries.

It is forbidden to enter event, normal log, span and Studio response: token, Authorization, Cookie, execution credentials, complete tool input/output, complete function code, stdout/stderr, external response, approval opinion, SQL, PII, target system sensitive payload.

Allowed logging: secure enums, length/count, full SHA-256, controlled action/tool/task/artifact/policy references. Errors only record the category and hash, not the original error text.

## 8. Given-When-Then Acceptance.

- Given that the recommendation and approval request have been submitted by the upstream, when the permission is passed and the execution is successful, Then only approved, executed, and result_recorded are added.
- Given permission denial, When entering the real permission boundary, Then only adds rejected and does not access execution dependencies.
- Given execution failed, When permission has been passed, the status of Then executed/result is error, and the original error text is not visible.
- Given the same operation/attempt replay, When the event is reconstructed, Then the event ID is consistent with the complete content.
- Given that the Action causality header is missing or the monitor test strategy is not revocable, When the tool is called, the Action event is not generated.
- Given multiple concurrent requests for the same action/attempt, when the side effect boundary is reached, Then only one gets execution rights.
- Given that the side effects have been completed but the final state evidence failed to be reported, When the client retries, Then the final state evidence is reissued and the cached result is returned, without executing the tool again.
- Given ingest returns a non-2xx or timeout, When the Action is submitted fact, Then bounded retries will be returned to the calling path on eventual failure.

## 9. Fixture and testing.

- Positive fixture: `fixtures/bkn-trace/action_2_1_positive.json`.
- Contract test: `server/infra/bkntrace/evidence_test.go`.
- Execute boundary testing: `server/logics/toolbox/execute_trace_test.go`.
- Verification command: `go test ./server/infra/bkntrace ./server/logics/toolbox`.

## 10. Reliability and current boundaries.

- This batch is first connected to toolbox `ExecuteTool`; MCP, operator proxy and sandbox will reuse the same helper later.
- The action path is fail-closed when the emitter is not configured, is not 2xx, times out, or approved is not confirmed, and no side effects are performed; ordinary non-Action tool calls maintain the original behavior.
- HTTP emitter uses bounded retry, and the original serialized event and timestamp remain unchanged.
- Redis gate does not set a TTL to prevent repeated side effects after restarting. If the process crashes after the side effects are completed but before the results are written to the gate, the status will remain in `executing` permanently and retry will be refused. Manual reconciliation is required; transactional execution log/outbox is not implemented in this batch.
- The upstream recommendation/approval request must first confirm the write; the operator does not modify the parent fact.
- Full local environment must point the ingest URL to the agent-observability real address.

## 11. Responsibility confirmation.

- Responsible party: OpenBKN Foundry/execution-factory.
- Review date: TBD.
- Compatibility risk: Medium; new headers are optional, but Action evidence events are only enabled in the full 2.1 context.
