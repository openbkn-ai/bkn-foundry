"""
Every runner's generated wrapper must be valid Python.

Wrappers are built with f-strings, so an escape written as \\n in the template
expands while generating and lands inside a string literal in the output. That
produces a SyntaxError only at execution time, on whichever runner is active.
"""

from pathlib import Path

import pytest

from executor.infrastructure.isolation.bwrap import BubblewrapRunner
from executor.infrastructure.isolation.code_wrapper import generate_python_wrapper

TOOL_CODE = (
    "from sandbox_sdk import tool\n"
    "@tool\n"
    "def add(a: int, b: int) -> int:\n"
    '    """两数相加"""\n'
    "    return a + b"
)
HANDLER_CODE = 'def handler(event):\n    return {"ok": True}'


@pytest.mark.parametrize("code", [TOOL_CODE, HANDLER_CODE], ids=["tool", "handler"])
def test_code_wrapper_output_compiles(code):
    compile(generate_python_wrapper(code), "<wrapper>", "exec")


@pytest.mark.parametrize("code", [TOOL_CODE, HANDLER_CODE], ids=["tool", "handler"])
def test_bwrap_wrapper_output_compiles(code, tmp_path):
    runner = BubblewrapRunner(tmp_path)
    compile(runner._generate_wrapper_code(code), "<wrapper>", "exec")


@pytest.mark.parametrize("code", [TOOL_CODE, HANDLER_CODE], ids=["tool", "handler"])
def test_bwrap_and_code_wrapper_pick_the_same_entry(code, tmp_path):
    """A given piece of code must not take different paths per runner."""
    runner = BubblewrapRunner(tmp_path)
    bwrap_uses_sdk = "sandbox_sdk.dispatch(event)" in runner._generate_wrapper_code(code)
    shared_uses_sdk = "sandbox_sdk.dispatch(event)" in generate_python_wrapper(code)

    assert bwrap_uses_sdk == shared_uses_sdk


def test_handler_wins_on_bwrap_too(tmp_path):
    both = TOOL_CODE + "\ndef handler(event):\n    return 1"
    runner = BubblewrapRunner(tmp_path)

    assert "result = handler(event)" in runner._generate_wrapper_code(both)
