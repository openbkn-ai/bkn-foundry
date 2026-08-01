// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mq

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/avast/retry-go"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/segmentio/kafka-go/sasl/scram"
)

const (
	Plain       = "PLAIN"
	ScramSHA256 = "SCRAM-SHA-256"
	ScramSHA512 = "SCRAM-SHA-512"
	ConnTimeout = 3 * time.Second
	IdleTimeout = 60 * time.Second
)

type OpenBKNKafkaClient struct {
	username string
	password string
	// Currently only support `PLAIN`, `SCRAM-SHA-256`, `SCRAM-SHA-512`
	mechanismProtocol string
	saslMechanism     sasl.Mechanism
	tlsConfig         *tls.Config
	brokers           []string
	writers           map[string]*kafka.Writer
	// 共享的 transport 实例，用于连接池
	transport  *kafka.Transport
	mu         sync.Mutex
	sharedConn bool
}

func NewKafkaClient(pubServer string, pubPort int, subServer string, subPort int) OpenBKNMQClient {
	addrs := strings.Split(strings.TrimSpace(pubServer), ",")
	brokers := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		brokers = append(brokers, fmt.Sprintf("%s:%d", parseHost(addr), pubPort))
	}

	// 创建一个共享的 transport 实例，用于连接池
	return &OpenBKNKafkaClient{
		brokers: brokers,
		writers: make(map[string]*kafka.Writer),
	}
}

func (kc *OpenBKNKafkaClient) initialize() (err error) {
	if kc.saslMechanism != nil {
		// 确保在测试等场景下零值结构体也能正常工作
		if kc.writers == nil {
			kc.writers = make(map[string]*kafka.Writer)
		}
		if kc.transport == nil {
			kc.transport = &kafka.Transport{
				DialTimeout: ConnTimeout,
				IdleTimeout: IdleTimeout,
				TLS:         kc.tlsConfig,
				SASL:        kc.saslMechanism,
			}
		}
		return nil
	}
	var m sasl.Mechanism
	switch kc.mechanismProtocol {
	case ScramSHA256:
		m, err = scram.Mechanism(scram.SHA256, kc.username, kc.password)
		if err != nil {
			return
		}
	case ScramSHA512:
		m, err = scram.Mechanism(scram.SHA512, kc.username, kc.password)
		if err != nil {
			return
		}
	case Plain:
		m = plain.Mechanism{Username: kc.username, Password: kc.password}
	default:
	}
	kc.saslMechanism = m

	if kc.writers == nil {
		kc.writers = make(map[string]*kafka.Writer)
	}

	// 初始化全局 transport
	kc.transport = &kafka.Transport{
		DialTimeout: ConnTimeout,
		IdleTimeout: IdleTimeout,
		TLS:         kc.tlsConfig,
		SASL:        kc.saslMechanism,
	}
	return nil
}

func (kc *OpenBKNKafkaClient) getWriter(topic string) *kafka.Writer {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	if writer, ok := kc.writers[topic]; ok {
		return writer
	}

	var writeTransport *kafka.Transport
	if kc.sharedConn {
		writeTransport = kc.transport
	} else {
		writeTransport = &kafka.Transport{
			// 设置合理的连接池超时时间
			DialTimeout: ConnTimeout,
			IdleTimeout: IdleTimeout,
			TLS:         kc.tlsConfig,
			SASL:        kc.saslMechanism,
		}
	}

	writer := &kafka.Writer{
		Addr:                   kafka.TCP(kc.brokers...),
		Topic:                  topic,
		Transport:              writeTransport,
		BatchSize:              1,
		AllowAutoTopicCreation: true,
	}

	kc.writers[topic] = writer
	return writer
}

func (kc *OpenBKNKafkaClient) createTopic(topic string) {
	dialer := &kafka.Dialer{
		Timeout:       ConnTimeout,
		DualStack:     true,
		SASLMechanism: kc.saslMechanism,
	}

	// 如果 auto.create.topics.enable=true，这将创建主题
	conn, err := dialer.DialLeader(context.Background(), "tcp", kc.brokers[0], topic, 0)
	if err != nil {
		log.Printf("connect kafka with topic %s failed, %v", topic, err)
		return
	}

	defer conn.Close()
}

func (kc *OpenBKNKafkaClient) Pub(topic string, msg []byte) (err error) {
	if err = kc.initialize(); err != nil {
		log.Printf("init kafka writer failed: %v", err)
		return
	}
	writer := kc.getWriter(topic)

	maxAttempts := uint(200)
	// 最长重试阻塞时间：10s
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return retry.Do(
		func() error {
			return writer.WriteMessages(context.Background(), kafka.Message{Value: msg})
		},
		retry.Attempts(maxAttempts),
		retry.Delay(500*time.Millisecond),
		retry.OnRetry(func(n uint, err error) {
			if n > 0 {
				log.Printf("failed to write msg - %v, retry %d times ...", err, n)
			}
		}),
		retry.RetryIf(func(err error) bool { return err != nil }),
		retry.MaxDelay(1*time.Second),
		retry.Context(ctx),
		retry.LastErrorOnly(true),
	)
}

func (kc *OpenBKNKafkaClient) Sub(topic string, channel string, handler MessageHandler, pollIntervalMilliseconds int64, maxInFlight int, opts ...SubOpt) (err error) {
	if err = kc.initialize(); err != nil {
		return
	}

	// fix issue: 664550 and 664554.
	kc.createTopic(topic)

	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  kc.brokers,
		GroupID:  channel,
		Topic:    topic,
		MinBytes: 10,   // 10B
		MaxBytes: 10e6, // 10MB
		MaxWait:  time.Duration(pollIntervalMilliseconds) * time.Millisecond,
		Dialer: &kafka.Dialer{
			TLS:           kc.tlsConfig,
			SASLMechanism: kc.saslMechanism,
			Timeout:       ConnTimeout,
		},
	})
	defer r.Close()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		for {
			m, err := r.FetchMessage(context.Background())
			if err != nil {
				log.Printf("read message failed: %+v", err)
				continue
			}
			if err := handler(m.Value); err == nil {
				if err := r.CommitMessages(context.Background(), m); err != nil {
					log.Printf("commit msg err: topic: %s, partition: %d, offset: %d", m.Topic, m.Partition, m.Offset)
				}
			}
		}
	}()
	<-sigChan
	log.Println("wait for consumer completed.")
	return
}

func (kc *OpenBKNKafkaClient) Close() {
	kc.mu.Lock()
	defer kc.mu.Unlock()

	// 关闭所有 writers
	for topic, writer := range kc.writers {
		if writer != nil {
			_ = writer.Close()
		}
		delete(kc.writers, topic)
	}

	// 关闭共享的 transport 的所有空闲连接
	if kc.transport != nil {
		kc.transport.CloseIdleConnections()
	}
}
