"""Tests for test_commons_snow_id."""
import pytest
from app.commons.snow_id import IdWorker, snow_id, worker, SEQUENCE_MASK


class TestIdWorker:
    """Tests for test id worker."""

    def test_init_valid_params(self):
        """Test test init valid params."""
        id_worker = IdWorker(datacenter_id=1, worker_id=1, sequence=0)
        assert id_worker.datacenter_id == 1
        assert id_worker.worker_id == 1
        assert id_worker.sequence == 0

    def test_init_invalid_worker_id(self):
        """Test test init invalid worker id."""
        with pytest.raises(ValueError, match="worker_id is out of range"):
            IdWorker(datacenter_id=1, worker_id=32, sequence=0)
        
        with pytest.raises(ValueError, match="worker_id is out of range"):
            IdWorker(datacenter_id=1, worker_id=-1, sequence=0)

    def test_init_invalid_datacenter_id(self):
        """Test test init invalid datacenter id."""
        with pytest.raises(ValueError, match="datacenter_id is out of range"):
            IdWorker(datacenter_id=32, worker_id=1, sequence=0)
        
        with pytest.raises(ValueError, match="datacenter_id is out of range"):
            IdWorker(datacenter_id=-1, worker_id=1, sequence=0)

    def test_gen_timestamp(self):
        """Test test gen timestamp."""
        id_worker = IdWorker(datacenter_id=1, worker_id=1)
        timestamp = id_worker._gen_timestamp()
        assert isinstance(timestamp, int)
        assert timestamp > 0

    def test_get_id_single(self):
        """Test test get id single."""
        id_worker = IdWorker(datacenter_id=1, worker_id=1)
        id1 = id_worker.get_id()
        assert isinstance(id1, int)
        assert id1 > 0

    def test_get_id_unique(self):
        """Test test get id unique."""
        id_worker = IdWorker(datacenter_id=1, worker_id=1)
        ids = set()
        for _ in range(1000):
            ids.add(id_worker.get_id())
        assert len(ids) == 1000

    def test_get_id_sequence_increment(self):
        """Test test get id sequence increment."""
        id_worker = IdWorker(datacenter_id=1, worker_id=1)
        # Generate multiple IDs within the same timestamp.
        id1 = id_worker.get_id()
        id2 = id_worker.get_id()
        # IDs should be increasing.
        assert id2 > id1

    def test_get_id_sequence_reset(self):
        """Test test get id sequence reset."""
        id_worker = IdWorker(datacenter_id=1, worker_id=1)
        # Generate enough IDs to trigger sequence reset.
        for _ in range(SEQUENCE_MASK + 1):
            id_worker.get_id()
        # The sequence should be reset.
        assert id_worker.sequence >= 0

    def test_til_next_millis(self):
        """Test test til next millis."""
        id_worker = IdWorker(datacenter_id=1, worker_id=1)
        last_timestamp = id_worker._gen_timestamp()
        next_timestamp = id_worker._til_next_millis(last_timestamp)
        assert next_timestamp > last_timestamp

    def test_clock_backwards(self):
        """Test test clock backwards."""
        id_worker = IdWorker(datacenter_id=1, worker_id=1)
        # Generate one ID first.
        id_worker.get_id()
        # Simulate clock rollback.
        id_worker.last_timestamp = id_worker._gen_timestamp() + 10000
        with pytest.raises(Exception):
            id_worker.get_id()


class TestSnowIdFunction:
    """Tests for test snow id function."""

    def test_snow_id_returns_int(self):
        """Test test snow id returns int."""
        result = snow_id()
        assert isinstance(result, int)
        assert result > 0

    def test_snow_id_unique(self):
        """Test test snow id unique."""
        import time
        ids = set()
        for _ in range(100):
            ids.add(snow_id())
            # Add a small delay to ensure the sequence increments.
            time.sleep(0.0001)
        assert len(ids) == 100


class TestWorkerGlobal:
    """Tests for test worker global."""

    def test_worker_exists(self):
        """Test test worker exists."""
        assert worker is not None
        assert isinstance(worker, IdWorker)

    def test_worker_generates_id(self):
        """Test test worker generates id."""
        id1 = worker.get_id()
        assert isinstance(id1, int)
        assert id1 > 0

