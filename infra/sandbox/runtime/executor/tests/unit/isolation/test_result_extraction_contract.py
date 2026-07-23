"""
Both result-extraction strategies must accept the same output.

SubprocessRunner takes the last valid JSON line on stdout; BubblewrapRunner and
MacSeatbeltRunner only look between ===SANDBOX_RESULT=== markers. Anything that
prints a result — the wrappers, and the infer-schema probe the execution factory
appends to user code — has to satisfy both, or it silently returns nothing on
whichever runner is active.
"""

import json

import pytest

from executor.infrastructure.isolation.result_parser import parse_return_value


def last_json_line(stdout: str):
    """SubprocessRunner's strategy."""
    lines = [line for line in stdout.strip().split("\n") if line.strip()]
    for line in reversed(lines):
        try:
            return json.loads(line)
        except json.JSONDecodeError:
            continue
    return None


def marked(payload: dict, preceding_output: str = "") -> str:
    return (
        preceding_output
        + "===SANDBOX_RESULT===\n"
        + json.dumps(payload)
        + "\n===SANDBOX_RESULT_END===\n"
    )


@pytest.mark.parametrize(
    "preceding_output",
    ["", "debug\n", 'a line that is itself json: {"x": 1}\n'],
    ids=["clean", "with-print", "with-json-print"],
)
def test_marked_output_satisfies_both_strategies(preceding_output):
    payload = {"supported": True, "name": "add"}
    stdout = marked(payload, preceding_output)

    assert parse_return_value(stdout) == payload
    assert last_json_line(stdout) == payload


def test_unmarked_output_is_invisible_to_marker_runners():
    """Why the markers are required — this is the failure mode being guarded."""
    stdout = json.dumps({"supported": True}) + "\n"

    assert last_json_line(stdout) == {"supported": True}
    assert parse_return_value(stdout) is None
