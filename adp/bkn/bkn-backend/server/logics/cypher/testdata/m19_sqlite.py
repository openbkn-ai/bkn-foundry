# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""JSON-lines, test-only SQL source. No product code or expected results imported."""
import json
import re
import sqlite3
import sys

connection = None
for line in sys.stdin:
    try:
        request = json.loads(line)
        if 'tables' in request:
            if connection:
                connection.close()
            sqlite3.register_converter('BKNBOOL', lambda raw: bool(int(raw)))
            sqlite3.register_converter('BKNJSON', json.loads)
            connection = sqlite3.connect(':memory:', detect_types=sqlite3.PARSE_DECLTYPES)
            connection.execute('PRAGMA case_sensitive_like=ON')
            connection.create_function('IF', 3, lambda test, yes, no: yes if test else no)
            connection.create_function('CONCAT', -1, lambda *args: None if None in args else ''.join(map(str, args)))
            for table in request['tables']:
                quote = lambda text: '"' + text.replace('"', '""') + '"'
                columns = table['columns']
                connection.execute('CREATE TABLE ' + quote(table['name']) + ' (' + ','.join(quote(c) + (' BKNBOOL' if c in table.get('bool_columns', []) else ' BKNJSON' if c in table.get('json_columns', []) else '') for c in columns) + ')')
                for row in table['rows']:
                    connection.execute('INSERT INTO ' + quote(table['name']) + ' VALUES (' + ','.join('?' for _ in columns) + ')', [json.dumps(row.get(c), allow_nan=False) if c in table.get('json_columns', []) and row.get(c) is not None else row.get(c) for c in columns])
            print(json.dumps({'ok': True}), flush=True)
        else:
            sql = re.sub(r'\{\{\.?([^}]+)\}\}', lambda m: '"' + m[1].replace('"', '""') + '"', request['sql'])
            cursor = connection.execute(sql)
            names = [column[0] for column in cursor.description]
            rows = [dict(zip(names, row)) for row in cursor.fetchall()]
            print(json.dumps({'entries': rows, 'columns': [{'name': name, 'type': 'any'} for name in names], 'total_count': len(rows)}, allow_nan=False), flush=True)
    except Exception as error:
        print(json.dumps({'adapter_error': type(error).__name__ + ': ' + str(error)}), flush=True)
