/******************************************************************************
* 版权信息：中电科金仓（北京）科技股份有限公司

* 作者：KingbaseES

* 文件名：ssl.go

* 功能描述：ssl认证相关接口

* 其它说明：

* 修改记录：
  1.修改时间：

  2.修改人：

  3.修改内容：

******************************************************************************/

package gokb

import (
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"net"
	"os"
	"os/user"
	"path/filepath"
)

// ssl基于sslmode和相关设置返回一个用于升级net.Conn的函数
func ssl(o values) (handler func(net.Conn) (nc net.Conn, err error), err error) {
	verifyCaOnly := false
	tlsConf := tls.Config{}
	switch mode := o["sslmode"]; mode {
	// 默认为"require"
	case "", "require":
		// 在Go 1.3版本之后TLS需要全验证，在此处跳过TLS的验证
		tlsConf.InsecureSkipVerify = true

		// 为了之前版本向后兼容
		// 如果根CA文件存在，sslmode=require的情况下的处理和verify-ca相同
		// 这意味着服务端证书对CA是有效的，我们不提倡依赖这种行为，需要证书验证的应用应该使用verify-ca或verify-full.
		if sslrootcert, ok := o["sslrootcert"]; ok {
			if _, err := os.Stat(sslrootcert); nil == err {
				verifyCaOnly = true
			} else {
				delete(o, "sslrootcert")
			}
		}
	case "verify-ca":
		tlsConf.InsecureSkipVerify = true
		verifyCaOnly = true
	case "verify-full":
		tlsConf.ServerName = o["host"]
	case "disable":
		return nil, nil
	default:
		return nil, fmterrorf(`unsupported sslmode %q; only "require" (default), "verify-full", "verify-ca", and "disable" supported`, mode)
	}

	err = sslClientCertificates(&tlsConf, o)
	if nil != err {
		return nil, err
	}
	err = sslCertificateAuthority(&tlsConf, o)
	if nil != err {
		return nil, err
	}

	// 接收由后端发起的重新协商请求
	// 重新协商在V8就已经弃用，但更早版本该选择的默认配置是启用的
	tlsConf.Renegotiation = tls.RenegotiateFreelyAsClient

	return func(conn net.Conn) (nc net.Conn, err error) {
		client := tls.Client(conn, &tlsConf)
		if verifyCaOnly {
			err = sslVerifyCertificateAuthority(client, &tlsConf)
			if nil != err {
				return nil, err
			}
		}
		return client, nil
	}, nil
}

// sslClientCertificates从用户目录的.kingbase目录获取sslcert和sslkey
// 这两个文件必须存在且有正确的权限
func sslClientCertificates(tlsConf *tls.Config, o values) (err error) {
	// user.Current()在交叉编译时可能会失败
	user, _ := user.Current()

	sslcert := o["sslcert"]
	if len(sslcert) == 0 && user != nil {
		sslcert = filepath.Join(user.HomeDir, ".kingbase", "kingbase.crt")
	}
	if len(sslcert) == 0 {
		return nil
	}
	if _, err = os.Stat(sslcert); os.IsNotExist(err) {
		return nil
	} else if nil != err {
		return err
	}

	sslkey := o["sslkey"]
	if 0 == len(sslkey) && nil != user {
		sslkey = filepath.Join(user.HomeDir, ".kingbase", "kingbase.key")
	}

	if 0 < len(sslkey) {
		if err := sslKeyPermissions(sslkey); nil != err {
			return err
		}
	}

	cert, err := tls.LoadX509KeyPair(sslcert, sslkey)
	if nil != err {
		return err
	}

	tlsConf.Certificates = []tls.Certificate{cert}
	return nil
}

// sslCertificateAuthority获取sslrootcert设置的RootCA
func sslCertificateAuthority(tlsConf *tls.Config, o values) (err error) {
	if sslrootcert := o["sslrootcert"]; len(sslrootcert) > 0 {
		tlsConf.RootCAs = x509.NewCertPool()

		cert, err := ioutil.ReadFile(sslrootcert)
		if nil != err {
			return err
		}

		if !tlsConf.RootCAs.AppendCertsFromPEM(cert) {
			return fmterrorf("couldn't parse pem in sslrootcert")
		}
	}

	return nil
}

// sslVerifyCertificateAuthority向后端发起TLS握手并根据CA验证当前的证书
// sslrootcert没有被指定时，则通过系统CA
func sslVerifyCertificateAuthority(client *tls.Conn, tlsConf *tls.Config) (err error) {
	err = client.Handshake()
	if nil != err {
		return err
	}
	certs := client.ConnectionState().PeerCertificates
	opts := x509.VerifyOptions{
		DNSName:       client.ConnectionState().ServerName,
		Intermediates: x509.NewCertPool(),
		Roots:         tlsConf.RootCAs,
	}
	for i, cert := range certs {
		if 0 == i {
			continue
		}
		opts.Intermediates.AddCert(cert)
	}
	_, err = certs[0].Verify(opts)
	return err
}
