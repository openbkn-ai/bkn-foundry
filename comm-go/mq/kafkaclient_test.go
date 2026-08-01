// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mq

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/segmentio/kafka-go/sasl/scram"
)

func TestNewKafkaClient(t *testing.T) {
	type args struct {
		pubServer string
		pubPort   int
		subServer string
		subPort   int
	}
	tests := []struct {
		name string
		args args
		want OpenBKNMQClient
	}{
		{
			name: "Normal",
			args: args{
				pubServer: "testPubServer",
				pubPort:   9092,
			},
			want: &OpenBKNKafkaClient{brokers: []string{"testPubServer:9092"}},
		},
		{
			name: "Ipv6Broker",
			args: args{
				pubServer: "::1",
				pubPort:   9092,
			},
			want: &OpenBKNKafkaClient{brokers: []string{"[::1]:9092"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewKafkaClient(tt.args.pubServer, tt.args.pubPort, tt.args.subServer, tt.args.subPort); !reflect.DeepEqual(got.(*OpenBKNKafkaClient).brokers, tt.want.(*OpenBKNKafkaClient).brokers) {
				t.Errorf("NewKafkaClient() = %v, want %v", got.(*OpenBKNKafkaClient).brokers, tt.want.(*OpenBKNKafkaClient).brokers)
			}
		})
	}
}

func TestOpenBKNKafkaClientInitialize(t *testing.T) {
	type fields struct {
		username          string
		password          string
		mechanismProtocol string
		saslMechanism     sasl.Mechanism
		tlsConfig         *tls.Config
		brokers           []string
	}
	var (
		user                 = "testuser"
		passwd               = "testpasswd"
		scram256Mechanism, _ = scram.Mechanism(scram.SHA256, user, passwd)
		scram512Mechanism, _ = scram.Mechanism(scram.SHA512, user, passwd)
	)
	tests := []struct {
		name              string
		fields            fields
		wantErr           error
		wantSaslMechanism sasl.Mechanism
	}{
		{
			name: "SaslPlain",
			fields: fields{
				username:          user,
				password:          passwd,
				mechanismProtocol: Plain,
			},
			wantSaslMechanism: plain.Mechanism{Username: user, Password: passwd},
		},
		{
			name: "SaslScramSha256",
			fields: fields{
				username:          user,
				password:          passwd,
				mechanismProtocol: ScramSHA256,
			},
			wantSaslMechanism: scram256Mechanism,
		},
		{
			name: "SaslScramSha512",
			fields: fields{
				username:          user,
				password:          passwd,
				mechanismProtocol: ScramSHA512,
			},
			wantSaslMechanism: scram512Mechanism,
		},
		{
			name: "ErrorUsername",
			fields: fields{
				username:          "u\tser",
				password:          "testpasswd",
				mechanismProtocol: ScramSHA512,
			},
			wantErr: fmt.Errorf("Error SASLprepping username '%s': prohibited character (rune: '\\u0009')", "u\tser"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kc := &OpenBKNKafkaClient{
				username:          tt.fields.username,
				password:          tt.fields.password,
				mechanismProtocol: tt.fields.mechanismProtocol,
				saslMechanism:     tt.fields.saslMechanism,
				tlsConfig:         tt.fields.tlsConfig,
				brokers:           tt.fields.brokers,
			}
			if err := kc.initialize(); err != nil && err.Error() != tt.wantErr.Error() {
				t.Errorf("OpenBKNKafkaClient.initialize() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestOpenBKNKafkaClientPub(t *testing.T) {
	type fields struct {
		username          string
		password          string
		mechanismProtocol string
		saslMechanism     sasl.Mechanism
		tlsConfig         *tls.Config
		brokers           []string
	}
	type args struct {
		topic string
		msg   []byte
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr error
		setup   func() *gomonkey.Patches // 不可直接声明patches，多个自测试之间会混淆，通过函数返回，在子测试中调用，作为局部变量打桩
	}{
		{
			name: "Success",
			fields: fields{
				mechanismProtocol: Plain,
				brokers:           []string{"127.0.0.1:9092"},
			},
			setup: func() (p *gomonkey.Patches) {
				p = gomonkey.ApplyMethod(reflect.TypeOf(&kafka.Writer{}), "WriteMessages", func(*kafka.Writer, context.Context, ...kafka.Message) error {
					log.Println("patch Success test")
					return nil
				})
				return
			},
		},
		{
			name: "ErrorWithRetry",
			fields: fields{
				mechanismProtocol: Plain,
				brokers:           []string{"127.0.0.1:9092"},
			},
			setup: func() (p *gomonkey.Patches) {
				// 使用单一打桩函数，避免 ApplyMethodSeq 在重试场景下出现 double seq panic。
				p = gomonkey.ApplyMethod(reflect.TypeOf(&kafka.Writer{}), "WriteMessages", func(*kafka.Writer, context.Context, ...kafka.Message) error {
					log.Println("patch ErrorWithRetry test")
					return kafka.BrokerIDNotRegistered
				})
				return
			},
			// Pub 在重试超时后会返回 context.DeadlineExceeded
			wantErr: context.DeadlineExceeded,
		},
		{
			name: "ErrorWithoutRetry",
			fields: fields{
				mechanismProtocol: Plain,
				brokers:           []string{"127.0.0.1:9092"},
			},
			setup: func() (p *gomonkey.Patches) {
				p = gomonkey.ApplyMethod(reflect.TypeOf(&kafka.Writer{}), "WriteMessages", func(*kafka.Writer, context.Context, ...kafka.Message) error {
					log.Println("patch Error test")
					return kafka.BrokerAuthorizationFailed
				})
				return
			},
			// Pub 在重试超时后会返回 context.DeadlineExceeded
			wantErr: context.DeadlineExceeded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer tt.setup().Reset()
			kc := &OpenBKNKafkaClient{
				username:          tt.fields.username,
				password:          tt.fields.password,
				mechanismProtocol: tt.fields.mechanismProtocol,
				saslMechanism:     tt.fields.saslMechanism,
				tlsConfig:         tt.fields.tlsConfig,
				brokers:           tt.fields.brokers,
			}
			t.Log(t.Name())
			if err := kc.Pub(tt.args.topic, tt.args.msg); err != nil && !errors.Is(tt.wantErr, err) {
				t.Errorf("OpenBKNKafkaClient.Pub() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestOpenBKNKafkaClientSub(t *testing.T) {
	// TODO currently have no idea for signal channel which used to exit sub function, so skip temporarily
	t.Skip()
	h := func(msg []byte) error {
		log.Printf("recieve msg: %s", *(*string)(unsafe.Pointer(&msg)))
		if *(*string)(unsafe.Pointer(&msg)) == "error msg" {
			return fmt.Errorf("error msg")
		}
		return nil
	}
	type fields struct {
		username          string
		password          string
		mechanismProtocol string
		saslMechanism     sasl.Mechanism
		tlsConfig         *tls.Config
		brokers           []string
	}
	type args struct {
		topic                    string
		channel                  string
		handler                  MessageHandler
		pollIntervalMilliseconds int64
		maxInFlight              int
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
		setup   func() *gomonkey.Patches
	}{
		{
			name: "case1",
			args: args{
				topic:   "testTopic",
				channel: "testG",
				handler: h,
			},
			fields: fields{
				username:          "testuser",
				password:          "testpasswd",
				mechanismProtocol: Plain,
			},
			setup: func() *gomonkey.Patches {
				return gomonkey.ApplyFunc(kafka.NewReader, func(kafka.ReaderConfig) *kafka.Reader {
					return &kafka.Reader{}
				}).ApplyMethod(reflect.TypeOf(&kafka.Reader{}), "Close", func(*kafka.Reader) error {
					return nil
				}).ApplyMethod(reflect.TypeOf(&kafka.Reader{}), "FetchMessage", func(*kafka.Reader, context.Context) (kafka.Message, error) {
					time.Sleep(5 * time.Second)
					return kafka.Message{Topic: "testTopic", Partition: 0, Offset: 11, Value: []byte("hello world.")}, nil
				}).ApplyMethod(reflect.TypeOf(&kafka.Reader{}), "CommitMessages", func(*kafka.Reader, context.Context, ...kafka.Message) error {
					return nil
				})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer tt.setup().Reset()
			kc := &OpenBKNKafkaClient{
				username:          tt.fields.username,
				password:          tt.fields.password,
				mechanismProtocol: tt.fields.mechanismProtocol,
				saslMechanism:     tt.fields.saslMechanism,
				tlsConfig:         tt.fields.tlsConfig,
				brokers:           tt.fields.brokers,
			}
			if err := kc.Sub(tt.args.topic, tt.args.channel, tt.args.handler, tt.args.pollIntervalMilliseconds, tt.args.maxInFlight); (err != nil) != tt.wantErr {
				t.Errorf("OpenBKNKafkaClient.Sub() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
