// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mq

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nsqio/go-nsq"
)

// OpenBKNNSQClient implement msq client interfaces base on nsq client library
type OpenBKNNSQClient struct {
	pubHTTPServer        string
	httpclient           *http.Client
	subLookupDHTTPServer string
}

// region deprecated
/*
// messageHandler implement nsq message handler interfaces
type messageHandler struct {
	handler func([]byte) error
}

// messageHandler.HandleMessage forward the message body to callback registered.
func (this *messageHandler) HandleMessage(m *nsq.Message) error {
	done := make(chan int)
	defer close(done)
	t := time.NewTicker(time.Second * 30)
	defer t.Stop()
	go func(msg *nsq.Message) {
		for {
			select {
			case <-t.C:
				log.Println("Touch message: ", msg.ID)
				msg.Touch()
			case <-done:
				return
			}
		}
	}(m)
	return this.handler(m.Body)
}
*/
// endregion

func (this *OpenBKNNSQClient) Close() {}

// NewNSQClient create a nsq client
//
// pubServer:pubPort should be nsqd http ip:port or [ip]:port
// subServer:subPort should be nsqlookupd http ip:port or [ip]:port
func NewNSQClient(pubServer string, pubPort int, subServer string, subPort int) OpenBKNMQClient {
	return &OpenBKNNSQClient{
		pubHTTPServer: fmt.Sprintf("http://%s:%d", parseHost(pubServer), pubPort),
		httpclient: &http.Client{
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 3 * time.Second}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
			Timeout: 3 * time.Second,
		},
		subLookupDHTTPServer: fmt.Sprintf("%s:%d", parseHost(subServer), subPort),
	}
}

func (this *OpenBKNNSQClient) createTopic(topic string) {
	log.Println("Try to create new topic", topic)
	for {
		body := bytes.NewBuffer([]byte(""))
		req, err := http.NewRequest("POST", fmt.Sprintf("%s/topic/create?topic=%s", this.pubHTTPServer, topic), body)
		if err == nil {
			req.Header.Set("User-Agent", "openbknmsq.nsqwrapper")
			req.Header.Set("Content-Type", "application/octet-stream")
			resp, err := this.httpclient.Do(req)
			if err == nil {
				_, _ = io.Copy(ioutil.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode == 200 {
					return
				}
				log.Println("Fail to create topic, server response:", resp)
			} else {
				log.Println("Fail to make http request:", err)
			}
		} else {
			log.Println("Fail to create http request:", err)
		}
		// retry after second
		time.Sleep(time.Second)
	}
}

func (this *OpenBKNNSQClient) createTopicChannel(topic string, channel string) {
	log.Println("Try to create new channel", channel, topic)
	for {
		body := bytes.NewBuffer([]byte(""))
		req, err := http.NewRequest("POST", fmt.Sprintf("http://%s/channel/create?topic=%s&channel=%s", this.subLookupDHTTPServer, topic, channel), body)
		if err == nil {
			req.Header.Set("User-Agent", "openbknmsq.nsqwrapper")
			req.Header.Set("Content-Type", "application/octet-stream")
			resp, err := this.httpclient.Do(req)
			if err == nil {
				_, _ = io.Copy(ioutil.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode == 200 {
					return
				}
				log.Println("Fail to create channel, server response:", resp)
			} else {
				log.Println("Fail to make http request:", err)
			}
		} else {
			log.Println("Fail to create http request:", err)
		}
		// retry after second
		time.Sleep(time.Second)
	}
}

// region core methods: pub, sub

// OpenBKNNSQClient.Pub send message to the specified topic on nsq server.
//
// Using nsq http api
func (this *OpenBKNNSQClient) Pub(topic string, msg []byte) error {
	body := bytes.NewBuffer(msg)
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/pub?topic=%s", this.pubHTTPServer, topic), body)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "openbknmsq.nsqwrapper")
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := this.httpclient.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(ioutil.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Receive unexpected http status code %d", resp.StatusCode)
	}
	return nil
}

// OpenBKNNSQClient.Sub start a blocking rounting, constantly polling message from nsq and forward the message to the registered handler
//
// pollIntervalMilliseconds in ms, control the interval of polling process, should be in range [1, 1000]
// maxInFlight control the concurrency the message handler, should be in range [1 256]
func (this *OpenBKNNSQClient) Sub(topic string, channel string, handler MessageHandler, pollIntervalMilliseconds int64, maxInFlight int, opts ...SubOpt) error {
	// create topic/channel first
	this.createTopic(topic)
	this.createTopicChannel(topic, channel)
	log.Println("start new consumer", topic, channel)
	cfg := nsq.NewConfig()
	cfg.MaxAttempts = 65535
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	cfg.UserAgent = "openbknmsq-nsqwrapper"
	cfg.MaxInFlight = maxInFlight
	// https://devops.aishu.cn/AISHUDevOps/AnyShareFamily/_workitems/edit/594429
	// too many poll interval will increase lookupd cpu usage
	if pollIntervalMilliseconds < 3000 {
		pollIntervalMilliseconds = 3000
	}
	if pollIntervalMilliseconds > 60000 {
		pollIntervalMilliseconds = 60000
	}
	cfg.LookupdPollInterval = time.Duration(pollIntervalMilliseconds) * time.Millisecond
	consumer, err := nsq.NewConsumer(topic, channel, cfg)
	if err != nil {
		return err
	}
	consumer.SetLoggerLevel(nsq.LogLevelError)
	concurrency := maxInFlight
	if concurrency > 256 {
		concurrency = 256
	}
	if concurrency < 1 {
		concurrency = 1
	}
	consumer.AddConcurrentHandlers(nsqMsgHandler(handler), concurrency)
	err = consumer.ConnectToNSQLookupds([]string{this.subLookupDHTTPServer})
	if err != nil {
		return err
	}
	<-sigChan
	fmt.Println("consumer terminating....")
	consumer.Stop()
	fmt.Println("wait on consumer....")
	<-consumer.StopChan
	fmt.Println("graceful shutdown done")
	return nil
}

// endregion

func nsqMsgHandler(h MessageHandler) nsq.Handler {
	return nsq.HandlerFunc(func(m *nsq.Message) error {
		done := make(chan int)
		defer close(done)
		t := time.NewTicker(time.Second * 30)
		defer t.Stop()
		go func(msg *nsq.Message) {
			for {
				select {
				case <-t.C:
					log.Println("Touch message: ", msg.ID)
					msg.Touch()
				case <-done:
					return
				}
			}
		}(m)
		return h(m.Body)
	})
}
