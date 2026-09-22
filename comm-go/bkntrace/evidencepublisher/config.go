package evidencepublisher

import "time"

type Config struct {
	Topic                 string
	ProducerID            string
	BaseStreamID          string
	WorkloadIdentity      string
	ProcessBootID         string
	CapturePolicyRevision string
	QueueMaxRecords       int
	QueueMaxBytes         int
	MaxRecordBytes        int
	MaxAge                time.Duration
	MaxAttempts           int
	RetryBackoff          time.Duration
	ShutdownTimeout       time.Duration
}

func (c Config) withDefaults() Config {
	if c.Topic == "" {
		c.Topic = Topic
	}
	if c.QueueMaxRecords == 0 {
		c.QueueMaxRecords = 4096
	}
	if c.QueueMaxBytes == 0 {
		c.QueueMaxBytes = 64 << 20
	}
	if c.MaxRecordBytes == 0 {
		c.MaxRecordBytes = 1 << 20
	}
	if c.MaxAge == 0 {
		c.MaxAge = 30 * time.Second
	}
	if c.MaxAttempts == 0 {
		c.MaxAttempts = 5
	}
	if c.RetryBackoff == 0 {
		c.RetryBackoff = 100 * time.Millisecond
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = 5 * time.Second
	}
	return c
}

func (c Config) validate() error {
	if c.ProducerID == "" || c.BaseStreamID == "" || c.WorkloadIdentity == "" || c.ProcessBootID == "" {
		return errInvalidConfig
	}
	if c.CapturePolicyRevision == "" {
		return errInvalidConfig
	}
	if c.QueueMaxRecords < 1 || c.QueueMaxBytes < 1 || c.MaxRecordBytes < 1 || c.MaxAttempts < 1 {
		return errInvalidConfig
	}
	return nil
}
