package evidencepublisher

import "time"

type queuedRecord struct {
	record     Record
	eventID    string
	enqueuedAt time.Time
	bytes      int
}
