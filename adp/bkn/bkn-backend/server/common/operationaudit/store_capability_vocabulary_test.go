// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package operationaudit

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// TestCapabilityBindingAuditVocabulary locks the audit vocabulary of capability binding (#1257).
//
// The store validates action and target_type against fixed sets and refuses anything else. A
// route rule in driveradapters is therefore not enough on its own, and the failure is invisible
// from the caller's side: the write succeeds, the audit row is dropped, and the only trace is a
// log line. This was found on the test server, not by the route-table test.
func TestCapabilityBindingAuditVocabulary(t *testing.T) {
	Convey("能力绑定的审计动作与目标类型已登记", t, func() {
		now := time.Now().UTC()
		base := Entry{
			EventID: "evt-1", EventTime: now, RecordedAt: now,
			KnowledgeNetworkID: "kn1", ActorID: "user-1", ActorName: "tester", ActorType: "user",
			RequestID: "req-1", TargetID: "bind-1", TargetName: "skill-1", Outcome: "success",
			TargetType: "kn_capability_binding",
		}

		for _, action := range []string{"attach", "detach"} {
			entry := base
			entry.Action = action
			So(validateEntry(entry), ShouldBeNil)
		}

		Convey("未登记的动作仍然被拒", func() {
			entry := base
			entry.Action = "mount"
			So(validateEntry(entry), ShouldNotBeNil)
		})
	})
}
