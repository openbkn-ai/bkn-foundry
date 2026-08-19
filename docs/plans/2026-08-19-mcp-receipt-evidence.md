# MCP Receipt Evidence Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Preserve Context Loader's authoritative MCP receipt evidence references so an internal bkn-agent Claim can cite retrieval provenance.

**Architecture:** Extend the bkn-agent receipt normalizer to accept the MCP `bkn_receipt` envelope and register only its validated observed evidence references against the current tool operation. Keep Context Loader as the sole retrieval-fact producer; missing, failed, or malformed receipts produce no candidate source.

**Tech Stack:** Python, pytest, bkn-agent evidence session state.

---

### Task 1: Demonstrate the missing MCP receipt adoption

**Files:**
- Modify: `infra/bkn-agent/app/test/test_evidence_reliability.py`
- Test: `infra/bkn-agent/app/test/test_evidence_reliability.py`

**Step 1: Write the failing test**

Create a test that passes a successful `bkn_receipt` containing `observed_evidence_refs` to `record_fact_receipt`, then asserts the matching tool message exposes the event in `bkn-candidate-source-event-ids`.

**Step 2: Run test to verify it fails**

Run: `pytest -q app/test/test_evidence_reliability.py -k mcp_receipt`

Expected: FAIL because `bkn_receipt` is not parsed.

### Task 2: Adopt only authoritative completed MCP receipt references

**Files:**
- Modify: `infra/bkn-agent/app/evidence.py`
- Test: `infra/bkn-agent/app/test/test_evidence_reliability.py`

**Step 1: Write failing edge-case tests**

Cover a failed/pending receipt and invalid references. Both must leave the candidate set empty.

**Step 2: Run tests to verify they fail**

Run: `pytest -q app/test/test_evidence_reliability.py -k mcp_receipt`

Expected: FAIL because receipt status and reference validation are not enforced.

**Step 3: Write minimal implementation**

Normalize `bkn_receipt` only when it is a completed durable receipt, validate every event id, and register each reference with the current operation and result context hash. Preserve existing header and toolbox receipt paths.

**Step 4: Run focused tests**

Run: `pytest -q app/test/test_evidence_reliability.py -k mcp_receipt`

Expected: PASS.

### Task 3: Verify regression boundaries

**Files:**
- Test: `infra/bkn-agent/app/test/test_evidence_reliability.py`
- Test: `infra/bkn-agent/app/test/test_toolbox_tools.py`

**Step 1: Run relevant suites**

Run: `pytest -q app/test/test_evidence_reliability.py app/test/test_toolbox_tools.py`

Expected: PASS.

**Step 2: Run lint and the bkn-agent test target**

Run the repository-supported bkn-agent checks from its module directory.

**Step 3: Commit**

Commit the focused implementation, tests, and this plan with a Conventional Commit message.
