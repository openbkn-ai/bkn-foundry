"""
Code wrapper generator for Lambda-style handler execution.

Generates wrapper code that injects the Lambda handler pattern around user code,
enabling execution via fileless `python3 -c` invocation. The wrapper reads event
data from stdin, calls the user's handler(event) function, and prints the return
value between special markers for extraction.
"""

import ast
import json
import re
from pathlib import Path
from typing import Optional

from executor.infrastructure.logging.logging_config import get_logger


logger = get_logger()

COMMON_SHELL_COMMANDS = {
    "awk",
    "cat",
    "cd",
    "chmod",
    "cp",
    "curl",
    "echo",
    "env",
    "find",
    "git",
    "go",
    "grep",
    "head",
    "java",
    "jq",
    "ls",
    "mkdir",
    "mv",
    "node",
    "npm",
    "pip",
    "pip3",
    "pnpm",
    "pwd",
    "pytest",
    "python",
    "python3",
    "rg",
    "rm",
    "sed",
    "sh",
    "tail",
    "uv",
    "yarn",
}


def defines_handler(code: str) -> bool:
    """
    Detect a module-level ``handler`` function.

    Args:
        code: User-supplied Python source

    Returns:
        True when the code defines ``handler``
    """
    try:
        tree = ast.parse(code)
    except SyntaxError:
        return False
    for item in ast.walk(tree):
        if isinstance(item, (ast.FunctionDef, ast.AsyncFunctionDef)) and item.name == "handler":
            return True
    return False


def uses_tool_decorator(code: str) -> bool:
    """
    Detect whether user code registers a function through ``sandbox_sdk``'s
    ``@tool`` decorator.

    The name alone is not enough: ``tool`` is also LangChain's decorator and is
    a plausible name for a user's own helper. Decorating with one of those and
    keeping a ``handler(event)`` would otherwise be dispatched through the SDK,
    which has no registration to find. So the name is only accepted when it was
    imported from — or accessed on — ``sandbox_sdk``.

    Parsing is AST-based so that ``@tool`` inside a comment or string is not a
    false positive, and unparsable code falls back to the handler path.

    Args:
        code: User-supplied Python source

    Returns:
        True when a function is decorated with sandbox_sdk's ``tool``
    """
    try:
        tree = ast.parse(code)
    except SyntaxError:
        return False

    # Names bound to sandbox_sdk's tool via `from sandbox_sdk import tool [as x]`
    sdk_tool_aliases = set()
    # Names bound to the module itself via `import sandbox_sdk [as x]`
    sdk_module_aliases = set()
    for item in ast.walk(tree):
        if isinstance(item, ast.ImportFrom) and item.module == "sandbox_sdk":
            for alias in item.names:
                if alias.name == "tool":
                    sdk_tool_aliases.add(alias.asname or alias.name)
        elif isinstance(item, ast.Import):
            for alias in item.names:
                if alias.name == "sandbox_sdk":
                    sdk_module_aliases.add(alias.asname or alias.name)

    def _is_sdk_tool(node: ast.AST) -> bool:
        # @tool(...) — unwrap to the callable being applied
        if isinstance(node, ast.Call):
            return _is_sdk_tool(node.func)
        # @tool, where tool came from sandbox_sdk
        if isinstance(node, ast.Name):
            return node.id in sdk_tool_aliases
        # @sandbox_sdk.tool
        if isinstance(node, ast.Attribute):
            return (
                node.attr == "tool"
                and isinstance(node.value, ast.Name)
                and node.value.id in sdk_module_aliases
            )
        return False

    for item in ast.walk(tree):
        if isinstance(item, (ast.FunctionDef, ast.AsyncFunctionDef)):
            for deco in item.decorator_list:
                if _is_sdk_tool(deco):
                    return True
    return False


def generate_python_wrapper(user_code: str) -> str:
    """
    Generate Python wrapper code around user code.

    Two execution modes are supported:

    1. ``@tool`` mode (sandbox_sdk): user writes a plain annotated function and
       the SDK unpacks ``event`` into named parameters. Selected when the code
       carries a ``@tool`` decorator.
    2. ``handler(event)`` mode (AWS Lambda style): the legacy contract, kept as
       the default so existing functions keep working unchanged.

    In both modes the wrapper reads the event JSON from stdin and prints the
    return value as the last JSON line on stdout, which is the contract the
    result parser relies on.

    Args:
        user_code: Python code containing either a ``@tool`` function or a
            ``handler(event)`` function

    Returns:
        Complete wrapped code ready for execution via python3 -c

    Examples:
        >>> user_code = 'def handler(event):\\n    return {"message": "Hello"}'
        >>> wrapped = generate_python_wrapper(user_code)
        >>> 'def handler(event):' in wrapped
        True
        >>> '===SANDBOX_RESULT===' in wrapped
        True
        >>> tool_code = '@tool\\ndef add(a: int, b: int) -> int:\\n    return a + b'
        >>> 'sandbox_sdk.dispatch(event)' in generate_python_wrapper(tool_code)
        True
    """
    # handler wins when both are present: it is the established contract, and a
    # @tool from another library would otherwise route to a dispatch with nothing
    # registered.
    if defines_handler(user_code) or not uses_tool_decorator(user_code):
        preamble = ""
        invoke = "handler(event)"
    else:
        preamble = "import sandbox_sdk"
        invoke = "sandbox_sdk.dispatch(event)"

    wrapper = f'''import sys
import json
import traceback
{preamble}

# User code starts here
{user_code}
# User code ends here

# Main execution wrapper
if __name__ == "__main__":
    try:
        # Read event from stdin
        stdin_data = sys.stdin.read()
        if stdin_data.strip():
            event = json.loads(stdin_data)
        else:
            event = {{}}

        # Invoke user function
        result = {invoke}

        # Print result between markers for extraction
        print("===SANDBOX_RESULT===")
        print(json.dumps(result))
        print("===SANDBOX_RESULT_END===")

    except Exception as e:
        # Print error to stderr
        traceback.print_exc()
        sys.exit(1)
'''
    return wrapper


def generate_javascript_wrapper(user_code: str) -> str:
    """
    Generate JavaScript wrapper code for Node.js execution.

    Args:
        user_code: JavaScript code to execute

    Returns:
        Complete wrapped code ready for execution via node -e
    """
    wrapper = f'''try {{
        // User code
        {user_code}
    }} catch (error) {{
        console.error(error);
        process.exit(1);
    }}
'''
    return wrapper


def generate_shell_wrapper(user_code: str) -> str:
    """
    Generate shell wrapper code for bash execution.

    Args:
        user_code: Shell script code to execute

    Returns:
        Complete wrapped code ready for execution via bash -c
    """
    # For shell, we mostly just pass through the code
    # but ensure it runs with proper error handling
    wrapper = f'''set -e  # Exit on error
{user_code}
'''
    return wrapper


def normalize_shell_code(user_code: str, cwd: Path) -> str:
    """
    Normalize shell code for compatibility with accidental `bash/sh <command>` input.

    The sandbox always executes shell content via `bash -c`. Some callers
    incorrectly prepend each command with `bash` or `sh`, producing forms like
    `bash python script.py` or `bash ls data/`. Those are not valid shell
    script invocations, so we strip the redundant interpreter prefix only for
    obvious command-style cases while preserving valid script-style invocations
    such as `bash run.sh` or `bash -lc "..."`.
    """
    segments = []
    current = []
    i = 0
    while i < len(user_code):
        if user_code.startswith("&&", i) or user_code.startswith("||", i):
            segments.append("".join(current))
            segments.append(user_code[i:i + 2])
            current = []
            i += 2
            continue
        if user_code[i] in ";&|\n":
            segments.append("".join(current))
            segments.append(user_code[i])
            current = []
            i += 1
            continue
        current.append(user_code[i])
        i += 1
    segments.append("".join(current))

    normalized_segments: list[str] = []
    stripped_flags: list[bool] = []
    separators = {"&&", "||", ";", "&", "|", "\n"}

    for segment in segments:
        if segment in separators:
            normalized_segments.append(segment)
            stripped_flags.append(False)
            continue

        normalized_segment, was_stripped = _normalize_shell_segment(segment, cwd)
        normalized_segments.append(normalized_segment)
        stripped_flags.append(was_stripped)

    for i, segment in enumerate(normalized_segments):
        if segment != "&":
            continue
        if i == 0 or i == len(normalized_segments) - 1:
            continue
        if stripped_flags[i - 1] and stripped_flags[i + 1]:
            normalized_segments[i] = "&&"

    return "".join(normalized_segments)


def _normalize_shell_segment(segment: str, cwd: Path) -> tuple[str, bool]:
    match = re.match(r"^(\s*)(bash|sh)(\s+)(\S+)", segment)
    if not match:
        return segment, False

    next_token = match.group(4)
    if not _should_strip_shell_prefix(next_token, cwd):
        return segment, False

    return match.group(1) + segment[match.start(4):], True


def _should_strip_shell_prefix(next_token: str, cwd: Path) -> bool:
    if next_token.startswith("-"):
        return False

    if next_token.endswith((".sh", ".bash")):
        return False

    if "/" in next_token:
        candidate = Path(next_token)
        if not candidate.is_absolute():
            candidate = cwd / candidate
        if candidate.exists() and candidate.is_file():
            return False
        return False

    if next_token in COMMON_SHELL_COMMANDS:
        return True

    candidate = cwd / next_token
    if candidate.exists() and candidate.is_file():
        return False

    return False


def validate_python_handler(code: str) -> tuple[bool, Optional[str]]:
    """
    Validate that Python code exposes an entry point.

    Accepts either entry style:
    - a ``@tool`` decorated function (sandbox_sdk), or
    - a ``handler(event)`` function (AWS Lambda style)

    Args:
        code: Python code to validate

    Returns:
        Tuple of (is_valid, error_message)
    """
    if not code or not code.strip():
        return False, "Code is empty"

    if "def handler(" not in code and not uses_tool_decorator(code):
        return False, (
            "No entry point found. Define a @tool decorated function "
            "or a handler(event) function."
        )

    # Basic syntax check
    try:
        compile(code, "<string>", "exec")
    except SyntaxError as e:
        return False, f"Syntax error: {e}"

    return True, None


def extract_handler_signature(code: str) -> Optional[str]:
    """
    Extract the handler function signature from code.

    Args:
        code: Python code containing handler function

    Returns:
        Function signature string, or None if not found

    Examples:
        >>> code = 'def handler(event, context=None):\\n    pass'
        >>> extract_handler_signature(code)
        'def handler(event, context=None)'
    """
    import re

    pattern = r"def handler\s*\((.*?)\):"
    match = re.search(pattern, code)
    if match:
        return f"def handler({match.group(1)}):"
    return None


def wrap_for_execution(code: str, language: str) -> str:
    """
    Generate appropriate wrapper for the given language.

    Args:
        code: User code to wrap
        language: Programming language (python, javascript, shell)

    Returns:
        Wrapped code ready for execution

    Raises:
        ValueError: If language is not supported
    """
    if language == "python":
        # Validate Python handler before wrapping
        is_valid, error = validate_python_handler(code)
        if not is_valid:
            logger.warning("Handler validation failed", error=error)
            # Still wrap it - let runtime error occur
        return generate_python_wrapper(code)

    elif language == "javascript":
        return generate_javascript_wrapper(code)

    elif language == "shell":
        return generate_shell_wrapper(code)

    else:
        raise ValueError(f"Unsupported language: {language}")


def unwrap_python_code(wrapped_code: str) -> str:
    """
    Extract original user code from wrapped Python code.

    This is useful for debugging or displaying the user's original code.

    Args:
        wrapped_code: Full wrapped Python code

    Returns:
        Original user code (between markers)
    """
    start_marker = "# User code starts here"
    end_marker = "# User code ends here"

    start_idx = wrapped_code.find(start_marker)
    if start_idx == -1:
        return wrapped_code

    end_idx = wrapped_code.find(end_marker, start_idx)
    if end_idx == -1:
        return wrapped_code

    # Extract code between markers
    code_start = start_idx + len(start_marker)
    code_end = end_idx
    return wrapped_code[code_start:code_end].strip()
