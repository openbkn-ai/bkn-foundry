// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

const detailLimit = 300

// WorkerLog keeps a two-second worker loop from repeating itself: a condition or
// failure is logged when it starts, when it changes and when it ends. Callers
// pass process states and identifiers only, never file names or document text.
type WorkerLog struct {
	Name          string
	Errorf, Infof func(string, ...any)
	failures      map[string]string
	conditions    map[string]string
}

// NewWorkerLog writes to the service log.
func NewWorkerLog(name string) *WorkerLog {
	return &WorkerLog{Name: name, Errorf: logger.Errorf, Infof: logger.Infof}
}

// Condition reports a standing situation; an empty message ends it. Pauses are
// informational, every other condition is an error an operator should see.
func (l *WorkerLog) Condition(key, message, ended string) {
	if l.conditions == nil {
		l.conditions = map[string]string{}
	}
	previous, active := l.conditions[key]
	switch {
	case message == "" && active:
		delete(l.conditions, key)
		l.Infof("kb %s worker: %s", l.Name, ended)
	case message != "" && previous != message:
		l.conditions[key] = message
		l.Infof("kb %s worker: %s", l.Name, message)
	}
}

// Problem is a Condition that needs attention, logged as an error when it starts.
func (l *WorkerLog) Problem(key string, err error) {
	if l.failures == nil {
		l.failures = map[string]string{}
	}
	if err == nil {
		delete(l.failures, key)
		return
	}
	if detail := safeDetail(err); l.failures[key] != detail {
		l.failures[key] = detail
		l.Errorf("kb %s worker: %s: %s", l.Name, key, detail)
	}
}

// Result reports the outcome of one step of an item.
func (l *WorkerLog) Result(item string, err error) {
	if l.failures == nil {
		l.failures = map[string]string{}
	}
	if _, failing := l.failures[item]; failing && err == nil {
		l.Infof("kb %s %s recovered", l.Name, item)
	}
	l.Problem(item, err)
}

// Transition reports committed state changes of one item.
func (l *WorkerLog) Transition(item string, changes []string) {
	for _, change := range changes {
		l.Infof("kb %s %s: %s", l.Name, item, change)
	}
}

// safeDetail bounds error text: a dependency's message may quote its response.
func safeDetail(err error) string {
	detail := strings.Join(strings.Fields(err.Error()), " ")
	if len(detail) > detailLimit {
		detail = strings.ToValidUTF8(detail[:detailLimit], "") + "..."
	}
	return detail
}
