// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package msqclient is a wrapper library for several message queue (msq) client libraries.
//
// client API for different msq may differ quite a lot.
// This package meant to wrap several complicated api calls into a simpified one, for some commonly used api.
package mq

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type MessageHandler func(msg []byte) error

// OpenBKNMQClient interface for simplified & commonly-used apis
type OpenBKNMQClient interface {
	// Pub send a message to the specified topic of msq
	Pub(topic string, msg []byte) error

	// Sub start consumers to subscribe and process message from specified topic/channel from the msg, the call would run
	// forever until the program is terminated
	Sub(topic string, channel string, handler MessageHandler, pollIntervalMilliseconds int64, maxInFlight int, opts ...SubOpt) error

	Close()
}

// Client connection options.

type ClientOpt func(client OpenBKNMQClient) error

// Set username and password for auth. Currently only supports for Kafka client.
func UserInfo(user, passwd string) ClientOpt {
	return func(client OpenBKNMQClient) error {
		switch (interface{})(client).(type) {
		case *OpenBKNKafkaClient:
			client.(*OpenBKNKafkaClient).username = user
			client.(*OpenBKNKafkaClient).password = passwd
			return nil
		default:
			return nil
		}
	}
}

// Mechanism of authentication, support `PLAIN`, `SCRAM-SHA-256`, `SCRAM-SHA-512`. Currently only supports for openbkn Kafka client.
func AuthMechanism(mechanism string) ClientOpt {
	return func(client OpenBKNMQClient) error {
		switch (interface{})(client).(type) {
		case *OpenBKNKafkaClient:
			if _, ok := map[string]struct{}{"PLAIN": {}, "SCRAM-SHA-256": {}, "SCRAM-SHA-512": {}}[strings.ToUpper(mechanism)]; !ok {
				err := fmt.Errorf("unsupported mechanism[%s] for kafka client.", mechanism)
				log.Println(err)
				return err
			}
			client.(*OpenBKNKafkaClient).mechanismProtocol = strings.ToUpper(mechanism)
			return nil
		default:
			return nil
		}
	}
}

// Load client certificate and key from certFile and keyFile into tlsConfig
func clientCert(tlsConfig *tls.Config, certFile, keyFile string) error {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("error loading client certificate: %w", err)
	}
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return fmt.Errorf("error parsing client certificate: %w", err)
	}
	if tlsConfig != nil {
		tlsConfig.MinVersion = tls.VersionTLS12
		tlsConfig.Certificates = []tls.Certificate{cert}
	}
	return nil
}

// Load root certificate from caFile into tlsConfig
func rootCAs(tlsConfig *tls.Config, caFile string) error {
	caCrt, err := os.ReadFile(caFile)
	if err != nil || caCrt == nil {
		return fmt.Errorf("error loading or parsing rootCA file: %w", err)
	}
	if ok := tlsConfig.RootCAs.AppendCertsFromPEM(caCrt); !ok {
		return fmt.Errorf("failed to parse root certificate from %q", caFile)
	}
	return nil
}

// If you are using a self-signed certificate for server,
// you need to set the absolute path of ca certificate file.
func RootCAs(caFile string) ClientOpt {
	return func(client OpenBKNMQClient) error {
		switch (interface{})(client).(type) {
		case *OpenBKNKafkaClient:
			if err := rootCAs(client.(*OpenBKNKafkaClient).tlsConfig, caFile); err != nil {
				return err
			}
		default:
			return nil
		}
		return nil
	}
}

// ClientCert is a helper option to provide the client certificate from a file.
func ClientCert(certFile, keyFile string) ClientOpt {
	return func(client OpenBKNMQClient) error {
		switch (interface{})(client).(type) {
		case *OpenBKNKafkaClient:
			if err := clientCert(client.(*OpenBKNKafkaClient).tlsConfig, certFile, keyFile); err != nil {
				return err
			}
		default:
			return nil
		}
		return nil
	}
}

// shareConn is a helper option to enable shared connection. only kafkaclient support this option.
func ShareConn(sharedConn bool) ClientOpt {
	return func(client OpenBKNMQClient) error {
		switch (interface{})(client).(type) {
		case *OpenBKNKafkaClient:
			client.(*OpenBKNKafkaClient).sharedConn = sharedConn
		default:
			return nil
		}
		return nil
	}
}

// endregion

// Subscription options.
type subOption struct {
	ackAsync bool // Acknowledge before consuming a message. Disabled by default.
}

type SubOpt func(opt *subOption) error

// set ackAsync to true
func AckAsync() SubOpt {
	return func(opt *subOption) error {
		opt.ackAsync = true
		return nil
	}
}

// endregion

type NewClienFn func(pubServer string, pubPort int, subServer string, subPort int) OpenBKNMQClient

// new client factory
var ncfFactory map[string]NewClienFn

func init() {
	ncfFactory = make(map[string]NewClienFn, 2)
	ncfFactory["nsq"] = NewNSQClient
	ncfFactory["kafka"] = NewKafkaClient
}

// NewOpenBKNMQClient create a msq connector for specified msqType
func NewOpenBKNMQClient(pubServer string, pubPort int, subServer string, subPort int, msqType string, opts ...ClientOpt) (OpenBKNMQClient, error) {
	if fn, ok := ncfFactory[msqType]; !ok {
		err := fmt.Errorf("unknown msq type %v", msqType)
		return nil, err
	} else {
		// Detect whether the caller provided the ShareConn option.
		hasShareConnOpt := false
		for i := range opts {
			// Function values are not comparable; their type names provide a practical
			// approximation for this option check.
			optStr := fmt.Sprintf("%T", opts[i])
			if strings.Contains(optStr, "ShareConn") {
				hasShareConnOpt = true
				break
			}
		}

		client := fn(pubServer, pubPort, subServer, subPort)
		// Enable shared connections by default when the option is omitted.
		if !hasShareConnOpt {
			if err := ShareConn(true)(client); err != nil {
				return nil, err
			}
		}

		var errs []error
		for _, opt := range opts {
			if e := opt(client); e != nil {
				errs = append(errs, e)
			}
		}
		return client, errors.Join(errs...)
	}
}

// config file struct define
type OpenBKNMQInfo struct {
	Host        string    `json:"mqHost" yaml:"mqHost"`
	Port        int       `json:"mqPort" yaml:"mqPort"`
	LookupdHost string    `json:"mqLookupdHost" yaml:"mqLookupdHost"`
	LookupdPort int       `json:"mqLookupdPort" yaml:"mqLookupdPort"`
	MQType      string    `json:"mqType" yaml:"mqType"`
	Auth        *AuthOpts `json:"auth,omitempty" yaml:"auth,omitempty"`
}

// AuthOpts contains authentication settings.
type AuthOpts struct {
	Username  string `json:"username" yaml:"username"`
	Password  string `json:"password" yaml:"password"`
	Mechanism string `json:"mechanism" yaml:"mechanism"`
}

// Create OpenBKNMQClient by reading infomations from config file.
func NewOpenBKNMQClientFromFile(configFile string) (OpenBKNMQClient, error) {
	fp, _ := filepath.Abs(configFile)
	if _, err := os.Stat(fp); err != nil {
		return nil, err
	}
	info := new(OpenBKNMQInfo)
	config, err := os.ReadFile(fp)
	if err != nil {
		return nil, err
	}
	err = yaml.Unmarshal(config, info)
	if err != nil {
		return nil, err
	}
	var opts []ClientOpt
	// Enable shared connections by default.
	opts = append(opts, ShareConn(true))
	if info.Auth != nil {
		opts = append(opts, AuthMechanism(info.Auth.Mechanism), UserInfo(info.Auth.Username, info.Auth.Password))
	}
	return NewOpenBKNMQClient(info.Host, info.Port, info.LookupdHost, info.LookupdPort, info.MQType, opts...)
}

func parseHost(host string) string {
	if strings.Contains(host, ":") {
		return fmt.Sprintf("[%s]", host)
	}
	return host
}
