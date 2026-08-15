// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mq

type MQAuthSetting struct {
	Username  string
	Password  string `json:"-"`
	Mechanism string
}

// MQSetting contains message queue configuration.
type MQSetting struct {
	MQType string
	MQHost string
	MQPort int
	Tenant string
	Auth   MQAuthSetting
}
