# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Join independent adapter reports onto every pinned M19 scenario exactly once.

Usage: python3 summarize_m19.py tableless-report.json graph-report.json output.json
Missing, rejected, unsupported and inapplicable scenarios are never passing cases.
"""
import collections
import json
from pathlib import Path
import sys
from generate_m19 import SHA256


def key(row):
    return tuple(row.get(name, row.get(name.lower())) for name in ('Source', 'Scenario', 'Example'))


def summarize(inventory, reports):
    if inventory['sha256'] != SHA256:
        raise ValueError('unexpected inventory source')
    known = {key(row): row for row in inventory['cases']}
    if len(known) != len(inventory['cases']):
        raise ValueError('duplicate scenario in inventory')
    observed = {}
    for report in reports:
        if report['SHA256'] != SHA256:
            raise ValueError('unexpected adapter source')
        for row in report['Cases']:
            identity = key(row)
            if identity not in known or identity in observed:
                raise ValueError('unknown scenario or overlapping adapter reports')
            observed[identity] = row
    counts, cases = collections.Counter(), []
    for row in inventory['cases']:
        item = {name: row[name] for name in ('source', 'scenario', 'example')}
        applicability = row['contract_applicability']
        if applicability['status'] in ('not_applicable', 'outside_read_only_scope'):
            if key(row) in observed:
                raise ValueError('an excluded scenario cannot be certified by an adapter')
            item.update(status=applicability['status'], detail=applicability['reason'])
        elif key(row) in observed:
            result = observed[key(row)]
            item.update(status=result['Status'], detail=result.get('Detail', ''))
        else:
            item.update(status='not_executed', detail=row['classification'])
        counts[item['status']] += 1
        cases.append(item)
    return {'sha256': SHA256, 'counts': dict(sorted(counts.items())), 'cases': cases}


def main():
    root = Path(__file__).parent
    inventory = json.loads((root / 'tck_m19_inventory.json').read_text())
    reports = [json.loads(Path(path).read_text()) for path in sys.argv[1:-1]]
    result = summarize(inventory, reports)
    Path(sys.argv[-1]).write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(result['counts'], sort_keys=True))


if __name__ == '__main__':
    main()
