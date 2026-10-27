# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Keep Python execution source off the operating system argument list."""

# Keep -c semantics (sys.path[0], __name__, and absence of __file__) while
# transmitting arbitrarily sized, product-limited source through a pipe.
PYTHON_STDIN_BOOTSTRAP = 'import sys; exec(compile(sys.stdin.buffer.read(), "<string>", "exec"))'
