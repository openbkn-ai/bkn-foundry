#!/usr/bin/env python3
"""
Remove empty per-conversation directories from a sandbox workspace.

Every conversation that runs code gets a working directory under the workspace
root (``conv-<conversation_id>``, or ``shared`` when no conversation is known).
The directory is the execution's working directory, so it has to exist before the
process starts -- it is created even when the code writes nothing. Over time the
root fills with directories that hold no files.

Only empty directories are removed, via ``rmdir``, which fails on a non-empty
directory. A directory modified more recently than ``--min-age-days`` is left
alone so that a live conversation is never pulled out from under a running
execution.

Usage:
    python3 prune_empty_workspaces.py /workspace --dry-run
    python3 prune_empty_workspaces.py /workspace --min-age-days 7
"""

import argparse
import os
import sys
import time
from pathlib import Path
from typing import Tuple

# Directory names the sandbox creates for a conversation's files.
WORKSPACE_PREFIXES = ("conv-",)
WORKSPACE_NAMES = ("shared",)


def is_conversation_workspace(path: Path) -> bool:
    """Report whether a directory is a per-conversation workspace."""
    name = path.name
    return name in WORKSPACE_NAMES or name.startswith(WORKSPACE_PREFIXES)


def prune(root: Path, min_age_days: float, dry_run: bool) -> Tuple[int, int]:
    """
    Remove empty conversation workspaces under root.

    Args:
        root: Workspace root to scan (not recursive; only its direct children)
        min_age_days: Skip directories modified within this many days
        dry_run: Report what would be removed without removing anything

    Returns:
        (removed, skipped) counts
    """
    cutoff = time.time() - min_age_days * 86400
    removed = 0
    skipped = 0

    for entry in sorted(root.iterdir()):
        if not entry.is_dir() or entry.is_symlink():
            continue
        if not is_conversation_workspace(entry):
            continue
        try:
            if any(entry.iterdir()):
                skipped += 1
                continue
            if entry.stat().st_mtime > cutoff:
                skipped += 1
                continue
            if dry_run:
                print(f"would remove {entry}")
            else:
                entry.rmdir()
                print(f"removed {entry}")
            removed += 1
        except OSError as exc:
            # A directory that became non-empty between the check and the rmdir,
            # or one this process cannot touch. Neither is worth failing over.
            print(f"skipped {entry}: {exc}", file=sys.stderr)
            skipped += 1

    return removed, skipped


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "root",
        nargs="?",
        default=os.environ.get("WORKSPACE_PATH", "/workspace"),
        help="Workspace root to scan (default: $WORKSPACE_PATH or /workspace)",
    )
    parser.add_argument(
        "--min-age-days",
        type=float,
        default=1.0,
        help="Leave directories modified within this many days (default: 1)",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Report what would be removed without removing anything",
    )
    args = parser.parse_args()

    root = Path(args.root)
    if not root.is_dir():
        print(f"workspace root is not a directory: {root}", file=sys.stderr)
        return 1

    removed, skipped = prune(root, args.min_age_days, args.dry_run)
    verb = "would remove" if args.dry_run else "removed"
    print(f"{verb} {removed} empty workspace(s), kept {skipped}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
