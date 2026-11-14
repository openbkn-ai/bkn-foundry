// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

const (
	ScheduleStatusNever      = "never"
	ScheduleStatusUnknown    = "unknown"
	ScheduleStatusTriggering = "triggering"
	ScheduleStatusSuccess    = "success"
	ScheduleStatusSkipped    = "skipped"
	ScheduleStatusFailed     = "failed"
)

// ScheduleRunResult 描述最近一次调度调用的结果，不代表已创建 Task 的执行结果。
type ScheduleRunResult struct {
	Status  string
	Message string
}
