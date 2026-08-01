// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mq

import (
	"testing"
)

// TestNewOpenBKNMQClientFactory 确认只保留 nsq / kafka 两种类型时工厂创建正常工作。
func TestNewOpenBKNMQClientFactory(t *testing.T) {
	type args struct {
		mqType string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name:    "nsq client",
			args:    args{mqType: "nsq"},
			wantErr: false,
		},
		{
			name:    "kafka client",
			args:    args{mqType: "kafka"},
			wantErr: false,
		},
		{
			name:    "unsupported type",
			args:    args{mqType: "bmq"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewOpenBKNMQClient("127.0.0.1", 1, "127.0.0.1", 1, tt.args.mqType)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewOpenBKNMQClient() error = %v, wantErr = %v", err, tt.wantErr)
			}
			if !tt.wantErr && client == nil {
				t.Fatalf("NewOpenBKNMQClient() client is nil for mqType=%s", tt.args.mqType)
			}
			if client != nil {
				client.Close()
			}
		})
	}
}
