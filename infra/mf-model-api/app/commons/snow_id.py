import time
# import logging

# Bit allocation for the 64-bit ID.
WORKER_ID_BITS = 5
DATACENTER_ID_BITS = 5
SEQUENCE_BITS = 12

# Maximum values for each segment.
MAX_WORKER_ID = -1 ^ (-1 << WORKER_ID_BITS)  # 2**5-1 0b11111
MAX_DATACENTER_ID = -1 ^ (-1 << DATACENTER_ID_BITS)

# Bit-shift offsets.
WOKER_ID_SHIFT = SEQUENCE_BITS
DATACENTER_ID_SHIFT = SEQUENCE_BITS + WORKER_ID_BITS
TIMESTAMP_LEFT_SHIFT = SEQUENCE_BITS + WORKER_ID_BITS + DATACENTER_ID_BITS

# Sequence rollover mask.
SEQUENCE_MASK = -1 ^ (-1 << SEQUENCE_BITS)

# Custom epoch based on Twitter's Snowflake epoch.
TWEPOCH = 1288834974657

# Optional application logger.
# logger = logging.getLogger('flask.app')


class IdWorker(object):
    """
    Generate Snowflake-style IDs.
    """

    def __init__(self, datacenter_id, worker_id, sequence=0):
        """
        Initialize the worker.
        :param datacenter_id: data-center ID
        :param worker_id: worker ID
        :param sequence: initial sequence number
        """
        # sanity check
        if worker_id > MAX_WORKER_ID or worker_id < 0:
            raise ValueError('worker_id is out of range')

        if datacenter_id > MAX_DATACENTER_ID or datacenter_id < 0:
            raise ValueError('datacenter_id is out of range')

        self.worker_id = worker_id
        self.datacenter_id = datacenter_id
        self.sequence = sequence

        self.last_timestamp = -1  # Timestamp used for the previous ID.

    def _gen_timestamp(self):
        """
        Return the current timestamp in milliseconds.
        :return:int timestamp
        """
        return int(time.time() * 1000)

    def get_id(self):
        """
        Generate the next ID.
        :return:
        """
        timestamp = self._gen_timestamp()

        # Detect clock rollback.
        if timestamp < self.last_timestamp:
            # logging.errors('clock is moving backwards. Rejecting requests until{}'.format(self.last_timestamp))
            raise Exception

        if timestamp == self.last_timestamp:
            self.sequence = (self.sequence + 1) & SEQUENCE_MASK
            if self.sequence == 0:
                timestamp = self._til_next_millis(self.last_timestamp)
        else:
            self.sequence = 0

        self.last_timestamp = timestamp

        new_id = ((timestamp - TWEPOCH) << TIMESTAMP_LEFT_SHIFT) | (self.datacenter_id << DATACENTER_ID_SHIFT) | \
                 (self.worker_id << WOKER_ID_SHIFT) | self.sequence
        return new_id

    def _til_next_millis(self, last_timestamp):
        """
        Wait until the next millisecond.
        """
        timestamp = self._gen_timestamp()
        while timestamp <= last_timestamp:
            timestamp = self._gen_timestamp()
        return timestamp


def snow_id():
    """Generate an ID with the process-wide worker."""
    idx = worker.get_id()
    return idx


worker = IdWorker(1, 1, 0)



