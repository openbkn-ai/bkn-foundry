package snowflake

import (
	"errors"
	"sync"
	"time"
)

const (
	// Bit allocation for the 64-bit ID.
	workerIDBits     = 5  // Worker ID bits.
	datacenterIDBits = 5  // Datacenter ID bits.
	sequenceBits     = 12 // Sequence bits.

	// Maximum values.
	maxWorkerID     = -1 ^ (-1 << workerIDBits)     // 31 (0b11111)
	maxDatacenterID = -1 ^ (-1 << datacenterIDBits) // 31 (0b11111)
	maxSequence     = -1 ^ (-1 << sequenceBits)     // 4095 (0b111111111111)

	// Bit offsets.
	workerIDShift      = sequenceBits                                   // 12
	datacenterIDShift  = sequenceBits + workerIDBits                    // 17
	timestampLeftShift = sequenceBits + workerIDBits + datacenterIDBits // 22

	// Twitter epoch timestamp (2010-11-04 09:42:54).
	twepoch int64 = 1288834974657
)

// IDWorker generates Snowflake IDs.
type IDWorker struct {
	mu            sync.Mutex
	datacenterID  int64
	workerID      int64
	sequence      int64
	lastTimestamp int64
}

// NewIDWorker creates a Snowflake generator. Both datacenterID and workerID
// must be in the inclusive range 0-31.
func NewIDWorker(datacenterID, workerID int64) (*IDWorker, error) {
	if workerID > maxWorkerID || workerID < 0 {
		return nil, errors.New("worker_id is out of range; expected 0-31")
	}

	if datacenterID > maxDatacenterID || datacenterID < 0 {
		return nil, errors.New("datacenter_id is out of range; expected 0-31")
	}

	return &IDWorker{
		datacenterID:  datacenterID,
		workerID:      workerID,
		sequence:      0,
		lastTimestamp: -1,
	}, nil
}

// GetID returns a new 19-digit Snowflake ID.
func (w *IDWorker) GetID() (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	timestamp := w.genTimestamp()

	// Reject clock rollback.
	if timestamp < w.lastTimestamp {
		return 0, errors.New("clock is moving backwards, rejecting requests")
	}

	// Advance the sequence within the same millisecond.
	if timestamp == w.lastTimestamp {
		w.sequence = (w.sequence + 1) & maxSequence
		if w.sequence == 0 {
			// Wait for the next millisecond after exhausting the sequence.
			timestamp = w.tilNextMillis(w.lastTimestamp)
		}
	} else {
		w.sequence = 0
	}

	w.lastTimestamp = timestamp

	// Layout: timestamp | datacenter ID | worker ID | sequence.
	newID := ((timestamp - twepoch) << timestampLeftShift) |
		(w.datacenterID << datacenterIDShift) |
		(w.workerID << workerIDShift) |
		w.sequence

	return newID, nil
}

// genTimestamp returns the current Unix timestamp in milliseconds.
func (w *IDWorker) genTimestamp() int64 {
	return time.Now().UnixNano() / 1e6
}

// tilNextMillis waits until the clock advances past lastTimestamp.
func (w *IDWorker) tilNextMillis(lastTimestamp int64) int64 {
	timestamp := w.genTimestamp()
	for timestamp <= lastTimestamp {
		timestamp = w.genTimestamp()
	}
	return timestamp
}

// The default worker uses datacenterID=1 and workerID=1.
var defaultWorker *IDWorker

func init() {
	var err error
	defaultWorker, err = NewIDWorker(1, 1)
	if err != nil {
		panic("failed to initialize snowflake worker: " + err.Error())
	}
}

// GetDefaultWorker returns the process-wide default worker.
func GetDefaultWorker() *IDWorker {
	return defaultWorker
}

// GenerateID generates an ID with the default worker.
func GenerateID() (int64, error) {
	return defaultWorker.GetID()
}
