/*
*****************************************************************************
* 版权信息：中电科金仓（北京）科技股份有限公司

* 作者：KingbaseES

* 文件名：scram.go

* 功能描述：SCRAM相关认证的实现

* 其它说明：

  - 修改记录：
    1.修改时间：

    2.修改人：

    3.修改内容：

*****************************************************************************
*/
package scram

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"hash"
	"strconv"
	"strings"
)

// SCRAM相关的认证方法(如SCRAM-SHA-1, SCRAM-SHA-256等).
// 可以在SASL会话中通过类似以下的方式进行使用:
//
//	var in []byte
//	var client = scram.NewClient(sha1.New, user, pass)
//	for client.Step(in) {
//	        out := client.Out()
//	        //发送到服务端
//	        in := serverOut
//	}
//	if client.Err() != nil {
//	        //认证失败
//	}
type Client struct {
	newHash func() hash.Hash

	user string
	pass string
	step int
	out  bytes.Buffer
	err  error

	clientNonce []byte
	serverNonce []byte
	saltedPass  []byte
	authMsg     bytes.Buffer
}

// NewClient返回一个包含提供的哈希算法的Client结构体
//
// 以SCRAM-SHA-256为例,用法如下:
//
//	client := scram.NewClient(sha256.New, user, pass)
func NewClient(newHash func() hash.Hash, userName, password string) (client *Client) {
	client = &Client{
		newHash: newHash,
		user:    userName,
		pass:    password,
	}
	client.out.Grow(256)
	client.authMsg.Grow(256)
	return
}

// Out返回当前要发送到服务端的数据
func (client *Client) Out() (bytes []byte) {
	if 0 == client.out.Len() {
		return nil
	} else {
		return client.out.Bytes()
	}
}

// Err返回出现的错误，无错误时返回空
func (client *Client) Err() (err error) {
	err = client.err
	return
}

// SetNonce将客户端nonce设置为提供的值
// 如果没有设置，则nonce会通过crypto/rand自动生成
func (client *Client) SetNonce(nonce []byte) {
	client.clientNonce = nonce
	return
}

var escaper = strings.NewReplacer("=", "=3D", ",", "=2C")

// Step处理来自服务器的传入数据，并通过Client.Out获取接下来所需的数据
// 如果没有错误且需要更多的数据，Step将返回false
func (client *Client) Step(in []byte) (state bool) {
	client.out.Reset()
	if 2 < client.step || nil != client.err {
		state = false
		return
	}
	client.step++
	switch client.step {
	case 1:
		client.err = client.step1(in)
	case 2:
		client.err = client.step2(in)
	case 3:
		client.err = client.step3(in)
	}
	state = (2 < client.step || nil != client.err)
	return
}

func (client *Client) step1(in []byte) (err error) {
	if 0 == len(client.clientNonce) {
		const nonceLen = 16
		buf := make([]byte, nonceLen+b64.EncodedLen(nonceLen))
		if _, readErr := rand.Read(buf[:nonceLen]); nil != readErr {
			return fmt.Errorf("cannot read random SCRAM-SHA-256 nonce from operating system: %v", readErr)
		}
		client.clientNonce = buf[nonceLen:]
		b64.Encode(client.clientNonce, buf[:nonceLen])
	}
	client.authMsg.WriteString("n=")
	escaper.WriteString(&client.authMsg, client.user)
	client.authMsg.WriteString(",r=")
	client.authMsg.Write(client.clientNonce)

	client.out.WriteString("n,,")
	client.out.Write(client.authMsg.Bytes())
	err = nil
	return
}

var b64 = base64.StdEncoding

func (client *Client) step2(in []byte) (err error) {
	client.authMsg.WriteByte(',')
	client.authMsg.Write(in)

	fields := bytes.Split(in, []byte(","))
	if 3 != len(fields) {
		err = fmt.Errorf("expected 3 fields in first SCRAM-SHA-256 server message, got %d: %q", len(fields), in)
		return
	}
	if !bytes.HasPrefix(fields[0], []byte("r=")) || 2 > len(fields[0]) {
		err = fmt.Errorf("server sent an invalid SCRAM-SHA-256 nonce: %q", fields[0])
		return
	}
	if !bytes.HasPrefix(fields[1], []byte("s=")) || 6 > len(fields[1]) {
		err = fmt.Errorf("server sent an invalid SCRAM-SHA-256 salt: %q", fields[1])
		return
	}
	if !bytes.HasPrefix(fields[2], []byte("i=")) || 6 > len(fields[2]) {
		err = fmt.Errorf("server sent an invalid SCRAM-SHA-256 iteration count: %q", fields[2])
		return
	}

	client.serverNonce = fields[0][2:]
	if !bytes.HasPrefix(client.serverNonce, client.clientNonce) {
		err = fmt.Errorf("server SCRAM-SHA-256 nonce is not prefixed by client nonce: got %q, want %q+\"...\"", client.serverNonce, client.clientNonce)
		return
	}

	salt := make([]byte, b64.DecodedLen(len(fields[1][2:])))
	n, decodeErr := b64.Decode(salt, fields[1][2:])
	if nil != decodeErr {
		err = fmt.Errorf("cannot decode SCRAM-SHA-256 salt sent by server: %q", fields[1])
		return
	}
	salt = salt[:n]
	iterCount, atoiErr := strconv.Atoi(string(fields[2][2:]))
	if nil != atoiErr {
		err = fmt.Errorf("server sent an invalid SCRAM-SHA-256 iteration count: %q", fields[2])
		return
	}
	client.saltPassword(salt, iterCount)

	client.authMsg.WriteString(",c=biws,r=")
	client.authMsg.Write(client.serverNonce)

	client.out.WriteString("c=biws,r=")
	client.out.Write(client.serverNonce)
	client.out.WriteString(",p=")
	client.out.Write(client.clientProof())
	err = nil
	return
}

func (client *Client) step3(in []byte) (err error) {
	var isv bool
	var ise bool
	fields := bytes.Split(in, []byte(","))
	if 1 == len(fields) {
		isv, ise = bytes.HasPrefix(fields[0], []byte("v=")), bytes.HasPrefix(fields[0], []byte("e="))
	}
	if ise {
		err = fmt.Errorf("SCRAM-SHA-256 authentication error: %s", fields[0][2:])
		return
	} else if !isv {
		err = fmt.Errorf("unsupported SCRAM-SHA-256 final message from server: %q", in)
		return
	}
	if !bytes.Equal(client.serverSignature(), fields[0][2:]) {
		err = fmt.Errorf("cannot authenticate SCRAM-SHA-256 server signature: %q", fields[0][2:])
		return
	}
	err = nil
	return
}

func (client *Client) saltPassword(salt []byte, iterCount int) {
	mac := hmac.New(client.newHash, []byte(client.pass))
	mac.Write(salt)
	mac.Write([]byte{0, 0, 0, 1})
	ui := mac.Sum(nil)
	hi := make([]byte, len(ui))
	copy(hi, ui)
	for i := 1; iterCount > i; i++ {
		mac.Reset()
		mac.Write(ui)
		mac.Sum(ui[:0])
		for j, b := range ui {
			hi[j] = hi[j] ^ b
		}
	}
	client.saltedPass = hi
	return
}

func (client *Client) clientProof() (bytes []byte) {
	mac := hmac.New(client.newHash, client.saltedPass)
	mac.Write([]byte("Client Key"))
	clientKey, hash := mac.Sum(nil), client.newHash()
	hash.Write(clientKey)
	storedKey := hash.Sum(nil)
	mac = hmac.New(client.newHash, storedKey)
	mac.Write(client.authMsg.Bytes())
	clientProof := mac.Sum(nil)

	for i, b := range clientKey {
		clientProof[i] = clientProof[i] ^ b
	}

	clientProof64 := make([]byte, b64.EncodedLen(len(clientProof)))
	b64.Encode(clientProof64, clientProof)
	bytes = clientProof64
	return
}

func (client *Client) serverSignature() (bytes []byte) {
	mac := hmac.New(client.newHash, client.saltedPass)
	mac.Write([]byte("Server Key"))
	serverKey := mac.Sum(nil)

	mac = hmac.New(client.newHash, serverKey)
	mac.Write(client.authMsg.Bytes())
	serverSignature := mac.Sum(nil)

	bytes = make([]byte, b64.EncodedLen(len(serverSignature)))
	b64.Encode(bytes, serverSignature)
	return
}
