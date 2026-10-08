import gzip
import tempfile
from pathlib import Path
import unittest

from core_snapshot import read_core_backup, export_core


class CoreBackupTests(unittest.TestCase):
    def read(self, text, compressed=False):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ("source.sql.gz" if compressed else "source.sql")
            if compressed:
                with gzip.open(path, "wb") as stream:
                    stream.write(text)
            else:
                path.write_bytes(text)
            return read_core_backup(path)

    def fixture(self):
        return b"USE `bkn_trace`;\nCREATE TABLE `bkn_trace_ee_current_explanations` (\n `interaction_id` varchar(80),\n `view_json` longblob,\n `created_at` datetime(6)\n);\nINSERT INTO `bkn_trace_ee_current_explanations` VALUES ('i1',0x00FF272C3B,'2026-09-01 01:02:03.123456'),('i2','raw\\0\\'\\\\\xff','2026-09-02 00:00:00');\nUSE `openbkn`; CREATE TABLE `checkpoint_writes` (\n `thread_id` varchar(80),\n `idx` bigint,\n `blob` blob\n); INSERT INTO `checkpoint_writes` (`blob`,`idx`,`thread_id`) VALUES (X'00FF',9223372036854775807,'t1');"

    def test_binary_bytes_extended_rows_and_bigint_are_preserved(self):
        for compressed in (False, True):
            rows, meta = self.read(self.fixture(), compressed)
            values = rows["bkn_trace.bkn_trace_ee_current_explanations"]
            self.assertEqual(len(values), 2)
            self.assertEqual(values[0]["view_json"], "00ff272c3b")
            self.assertEqual(values[1]["view_json"], b"raw\0'\\\xff".hex())
            self.assertEqual(values[0]["created_at"], "2026-09-01 01:02:03.123456")
            self.assertEqual(
                rows["openbkn.checkpoint_writes"][0]["idx"], 9223372036854775807
            )
            self.assertEqual(rows["openbkn.checkpoint_writes"][0]["blob"], "00ff")
            self.assertEqual(meta["tables"][0]["binary_columns"], ["view_json"])

    def test_strict_failure_never_drops_malformed_rows(self):
        for ending in [
            b"('bad',UNHEX('ff'),'time');",
            b"('bad',0xF,'time');",
            b"('bad','x');",
            b"('bad','x','time')",
        ]:
            prefix = self.fixture().split(b"INSERT INTO")[0]
            with self.subTest(ending=ending), self.assertRaises(ValueError):
                self.read(
                    prefix
                    + b"INSERT INTO `bkn_trace_ee_current_explanations` VALUES "
                    + ending
                )

    def test_text_encoding_is_strict_and_sql_mode_is_explicit(self):
        with self.assertRaises(ValueError):
            self.read(self.fixture().replace(b"'i1'", b"'\xff'"))
        with self.assertRaises(ValueError):
            self.read(b"SET SQL_MODE='NO_BACKSLASH_ESCAPES';" + self.fixture())

    def test_unsupported_dump_charset_and_sql_expressions_fail(self):
        for prefix in [
            b"/*!40101 SET NAMES latin1 */;",
            b"SET SQL_MODE='ANSI_QUOTES';",
        ]:
            with self.subTest(prefix=prefix), self.assertRaises(ValueError):
                self.read(prefix + self.fixture())
        with self.assertRaises(ValueError):
            self.read(self.fixture().replace(b"0x00FF272C3B", b"X'00 FF'"))

    def test_live_capture_uses_hex_and_preserves_field_names(self):
        class Source:
            def __init__(self):
                self.queries = []

            def query(self, sql):
                self.queries.append(sql)
                if "information_schema.COLUMNS" in sql:
                    if "TABLE_NAME='bkn_trace_ee_current_explanations'" in sql:
                        return [
                            "interaction_id\tvarchar",
                            "view_json\tlongblob",
                            "created_at\tdatetime",
                        ]
                    return []
                return [
                    '{"locator":"bkn_trace.bkn_trace_ee_current_explanations","row":{"interaction_id":"i1","view_json":"00FF","created_at":"2026-09-01 01:02:03.000000"}}'
                ]

        source = Source()
        rows, meta = export_core(source)
        self.assertEqual(
            rows["bkn_trace.bkn_trace_ee_current_explanations"][0]["view_json"], "00ff"
        )
        self.assertTrue(any("HEX(`view_json`)" in q for q in source.queries))
        self.assertTrue(
            all("INSERT" not in q and "UPDATE" not in q for q in source.queries)
        )
