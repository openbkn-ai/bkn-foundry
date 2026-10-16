"""Check the OSDK actually installed in a sandbox template during its build."""

import inspect
import json
import re
import sys
from importlib import metadata
from pathlib import Path

import bkn_osdk.schema as schema_module


def main(requirements_path: str) -> None:
    requirements = Path(requirements_path).read_text()
    match = re.search(
        r"^bkn-osdk @ (https://github\.com/openbkn-ai/bkn-sdk/archive/"
        r"([0-9a-f]{40})\.zip)#subdirectory=python$",
        requirements,
        re.MULTILINE,
    )
    if match is None:
        raise RuntimeError("OSDK must be pinned to a complete archive commit")
    distribution = metadata.distribution("bkn-osdk")
    direct_url = json.loads(distribution.read_text("direct_url.json") or "{}")
    if direct_url.get("url") != match[1] or direct_url.get("subdirectory") != "python":
        raise RuntimeError("Installed OSDK source does not match the template pin")
    installed_schema = Path(distribution.locate_file("bkn_osdk/schema.py")).resolve()
    if Path(schema_module.__file__).resolve() != installed_schema:
        raise RuntimeError("Another OSDK installation shadows the template package")
    schema = schema_module.parse_schema(
        {"id": "build-check"},
        [{"id": "unbound", "data_properties": None, "primary_keys": None}],
        [],
    )
    if schema.object_types[0].properties != () or schema.object_types[0].primary_key != ():
        raise RuntimeError("Installed OSDK does not handle nullable schema lists")
    print(
        json.dumps(
            {
                "osdk_commit": match[2],
                "schema_contract_reads": (
                    4
                    if "object_metrics" in inspect.signature(schema_module.parse_schema).parameters
                    else 3
                ),
                "nullable_schema_lists": "passed",
            }
        )
    )


if __name__ == "__main__":
    main(sys.argv[1])
