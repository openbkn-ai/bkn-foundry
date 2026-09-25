"""Reconciler-only results for non-publish manifest classifications."""


def reconcile(entries, ledger_by_event):
    """Return only results the Consumer is not authorized to write.

    `verify_delivered` requires a matching durable Ledger record; `coverage_gap`
    is the manifest-admin's immutable conclusion. Publish entries are omitted
    because Consumer owns their result lifecycle.
    """
    results = []
    for entry in entries:
        classification = entry["classification"]
        if classification == "publish":
            continue
        if classification == "verify_delivered":
            ledger = ledger_by_event.get(entry["event_id"])
            if ledger is None or ledger.get("payload_hash") != entry["payload_hash"]:
                continue
            results.append({"entry_id": entry["entry_id"], "adjudication": "verified_delivered", "reason_code": entry["classification_reason"]})
        elif classification == "coverage_gap":
            results.append({"entry_id": entry["entry_id"], "adjudication": "coverage_gap", "reason_code": entry["classification_reason"]})
        else:
            raise ValueError("unknown immutable manifest classification")
    return results
