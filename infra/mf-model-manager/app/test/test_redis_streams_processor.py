"""Tests for test_redis_streams_processor."""
import json

import pytest
from unittest.mock import AsyncMock, MagicMock, patch


def _make_processor():
    with patch('app.utils.redis_streams_processor.QuotaAggregator') as mock_agg_cls:
        from app.utils.redis_streams_processor import RedisStreamsProcessor
        proc = RedisStreamsProcessor()
        proc.aggregator = mock_agg_cls.return_value
    return proc


class TestHandleEntry:
    def test_bytes_value_parsed_and_aggregated(self):
        proc = _make_processor()
        payload = {'model_id': 'm1', 'user_id': 'u1', 'status': 'success'}
        proc._handle_entry(b'1-0', {b'value': json.dumps(payload).encode('utf-8')})

        proc.aggregator.add_record.assert_called_once_with(payload)

    def test_str_value_key_supported(self):
        proc = _make_processor()
        payload = {'model_id': 'm1', 'user_id': 'u1', 'status': 'success'}
        proc._handle_entry('1-0', {'value': json.dumps(payload)})

        proc.aggregator.add_record.assert_called_once_with(payload)

    def test_missing_value_field_skipped(self):
        proc = _make_processor()
        proc._handle_entry(b'1-0', {b'other': b'x'})

        proc.aggregator.add_record.assert_not_called()

    def test_bad_json_does_not_raise(self):
        proc = _make_processor()
        proc._handle_entry(b'1-0', {b'value': b'not-json'})

        proc.aggregator.add_record.assert_not_called()

    def test_aggregator_error_does_not_raise(self):
        proc = _make_processor()
        proc.aggregator.add_record.side_effect = Exception("price cache miss")
        proc._handle_entry(b'1-0', {b'value': b'{"model_id":"m1"}'})


class TestConsumeEntries:
    def test_returns_ack_ids(self):
        proc = _make_processor()
        entries = [(b'stream', [
            (b'1-0', {b'value': b'{"a":1}'}),
            (b'1-1', {b'value': b'{"a":2}'}),
        ])]

        ack_ids = proc._consume_entries(entries)

        assert ack_ids == [b'1-0', b'1-1']
        assert proc.aggregator.add_record.call_count == 2


class TestEnsureGroup:
    @pytest.mark.asyncio
    async def test_busygroup_tolerated(self):
        proc = _make_processor()
        proc.conn = MagicMock()
        proc.conn.xgroup_create = AsyncMock(
            side_effect=Exception("BUSYGROUP Consumer Group name already exists"))

        # Should not raise.
        await proc._ensure_group()

    @pytest.mark.asyncio
    async def test_other_error_raises(self):
        proc = _make_processor()
        proc.conn = MagicMock()
        proc.conn.xgroup_create = AsyncMock(side_effect=Exception("NOAUTH"))

        with pytest.raises(Exception):
            await proc._ensure_group()


class TestAutoclaim:
    @pytest.mark.asyncio
    async def test_claimed_entries_processed_and_acked(self):
        proc = _make_processor()
        proc.conn = MagicMock()
        proc.conn.xautoclaim = AsyncMock(return_value=(
            b'0-0',
            [
                (b'1-0', {b'value': b'{"model_id":"m1","user_id":"u1","status":"success"}'}),
                (b'1-1', None),  # Message already truncated by XTRIM.
            ],
        ))
        proc.conn.xack = AsyncMock()

        await proc._autoclaim_stale_pending()

        proc.aggregator.add_record.assert_called_once()
        proc.conn.xack.assert_awaited_once()
        # Only ACK entries that have content.
        assert proc.conn.xack.call_args[0][2:] == (b'1-0',)

    @pytest.mark.asyncio
    async def test_autoclaim_error_swallowed(self):
        proc = _make_processor()
        proc.conn = MagicMock()
        proc.conn.xautoclaim = AsyncMock(side_effect=Exception("ERR unknown command"))

        # Older Redis versions have no XAUTOCLAIM: warn only and do not affect the main loop.
        await proc._autoclaim_stale_pending()
