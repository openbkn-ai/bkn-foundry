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
	switch mode := o["sslmode"]; mode {
	case "", "require", "verify-ca", "verify-full":
	case "disable":
		return nil, nil
	default:
		return nil, fmterrorf(`unsupported sslmode %q; only "require" (default), "verify-full", "verify-ca", and "disable" supported`, mode)
	}

	// All TLS modes validate both the certificate chain and server name. Private
	// deployments can provide their CA through sslrootcert.
	tlsConf := tls.Config{ServerName: o["host"]}

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
		return tls.Client(conn, &tlsConf), nil
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
