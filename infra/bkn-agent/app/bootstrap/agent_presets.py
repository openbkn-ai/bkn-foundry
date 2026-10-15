# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Common preset command; app.bootstrap.studio remains a compatible entry point."""

from app.bootstrap.studio import main

if __name__ == "__main__":
    raise SystemExit(main())
