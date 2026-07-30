# go-msq
## Motivations
go-msq is a simple wrapper library for several kinds of message queue

The purpose of the wrapper is to simplify the message queue api, and the simplified version of apis are used in following manner

## Functions
The core functions are send messages to message queue and consume messages those recieve from message queue. In terms of use, some functional scenarios are also supported. The function summary is as follows:
function|describe
--|--
publish|Send messages to message queue
subscribe|Consume messages those recieve from message queue
SASL|Simple Authentication and Security Layer supported, but only apply for kafka. Currently support `PLAIN`, `SCRAM-SHA-256`, `SCRAM-SHA-512`.

## Message Queue Versions
Message Queue Type | Versions
:--:|:--:
kafka|2.3.1~3.4.0
nsq|2.x

## Go version
go-msq requires Go version 1.16 or later.

## OpenBKN MQ Client
The `OpenBKNMQClient` interface defines the core method of publish/subscribe messages action.The `NewOpenBKNMQClient` create the client which implemented `OpenBKNMQClient` according to `mqType`.

import sdk use 
```go
import msqclient "github.com/openbkn-ai/bkn-comm-go/mq"
```

Available value of variable `mqType`:
* nsq
* kafka

For example:
```go
client, err := msqclient.NewOpenBKNMQClient("openbkn-mq-nsq-nsqd.resource", 4151,"openbkn-mq-nsq-nsqlookupd.resource", 4161, "nsq")
if err != nil {
    log.Fatal("failed to create a openbkn mq client:", err)
}

// also you can Create OpenBKNMQClient using configfile
// File content format, please see examples/config.yaml
client, err := msqclient.NewOpenBKNMQClientFromFile("/path/to/configfile")
if err != nil {
    log.Fatal("failed to create a openbkn mq client:", err)
}
```

Note that it is important to call `Close()` on a `OpenBKNMQClient` when you do not need to call `Pub(...)`, which intend to close and release the producer connections.
```go
// to publish messages
// notice: when using besmq, pubServerPort and subServerIP is a string composite of ips split by comma.
c, err :=  msqclient.NewOpenBKNMSQClient(pubServerIP, pubServerPort, subServerIP, subServerPort, msqType)
if err != nil {
    log.Fatal("failed to create a openbkn mq client:", err)
}
defer c.Close()

func pub(message []byte) {
    c.Pub(topic, []byte("message hello world"))
}
for i:=0; i<10000; i++ {
    pub([]byte("hello world"))
}
```
```go
// to subscribe messages
c, err :=  msqclient.NewOpenBKNMSQClient(pubServerIP, pubServerPort, subServerIP, subServerPort, msqType)
if err != nil {
    log.Fatal("failed to create a openbkn mq client:", err)
}
// sub would block current gorountine until process is killed, gracefully shutdown for unfinished message is handled
// start a loop to fetch-process-ack messages
c.Sub(topic, channel, callback, pollInterval, maxInFlight) 
```
// start a loop to fetch-ack-process messages with ackAsync
c.Sub(topic, channel, callback, pollInterval, maxInFlight, msqclient.AckAsync()) 
## SASL Support
You can specify options on the `OpenBKNMQClient` to use SASL authentication, but it will only apply when use kafka.
### SASL Authentication Types
SASL Authentication support `PLAIN`, `SCRAM-SHA-256`, `SCRAM-SHA-512`, there is some example to specify options on client to use SASL authentication.
#### PLAIN
```go
username := "testUser"
password := "testPasswd"
opts := []msqclientClientOpt{msqclient.UserInfo(username, password), msqclient.AuthMechanism("PLAIN")}

client, err := msqclient.NewOpenBKNMQClient("openbkn-mq-nsq-nsqd.resource", 4151,"openbkn-mq-nsq-nsqlookupd.resource", 4161, "nsq", opts...)

if err != nil {
    log.Fatal("failed to create a openbkn mq client:", err)
}
```
#### SCRAM
```go
username := "testUser"
password := "testPasswd"
// when use SCRAM-SHA-256
opts := []msqclient.ClientOpt{msqclient.UserInfo(username, password), msqclient.AuthMechanism("SCRAM-SHA-256")}
// when use SCRAM-SHA-512
// opts := []msqclient.ClientOpt{msqclient.UserInfo(username, password), msqclient.AuthMechanism("SCRAM-SHA-512")}

client, err := msqclient.NewOpenBKNMQClient("openbkn-mq-nsq-nsqd.resource", 4151,"openbkn-mq-nsq-nsqlookupd.resource", 4161, "nsq", opts...)

if err != nil {
    log.Fatal("failed to create a openbkn mq client:", err)
}
```

## Integrated Development Enviroment
go-msq is a private repository, so you needed to configure your development enviroment follow the steps below:
1. Set go env
```bash
go env -w GO111MODULE=on
go env -w GOPROXY=https://goproxy.cn
go env -w GOPRIVATE=devops.aishu.cn
go env -w CGO_ENABLED="1"
```
2. Save your access credential of `devops.aishu.cn` into `.netrc` file, you can get token from [devops-token]
```bash
 echo "machine devops.aishu.cn login ${your_username} password ${your_access_token}" >> ~/.netrc
```

3. Use `ssh://` instead of `https://xxx`
```bash
git config --global url."ssh://devops.aishu.cn:22/AISHUDevOps/".insteadOf "https://devops.aishu.cn/AISHUDevOps/"
```

<!-- all links defines -->
[pipline]: https://devops.aishu.cn/AISHUDevOps/ICT/_build?definitionId=1979
[devops-token]: https://devops.aishu.cn/AISHUDevOps/_usersSettings/tokens
