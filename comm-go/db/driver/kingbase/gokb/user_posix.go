//go:build aix || darwin || dragonfly || freebsd || linux || nacl || netbsd || openbsd || solaris || rumprun
// +build aix darwin dragonfly freebsd linux nacl netbsd openbsd solaris rumprun

/*
*****************************************************************************
* 版权信息：中电科金仓（北京）科技股份有限公司

* 作者：KingbaseES

* 文件名：user_posix.go

* 功能描述：获取当前USER环境变量值

* 其它说明：

  - 修改记录：
    1.修改时间：

    2.修改人：

    3.修改内容：

*****************************************************************************
*/
package gokb

import (
	"os"
	"os/user"
)

func userCurrent() (s string, err error) {
	u, err := user.Current()
	if nil == err {
		s = u.Username
		err = nil
		return
	}

	name := os.Getenv("USER")
	if "" != name {
		s = name
		err = nil
		return name, nil
	}
	s = ""
	err = ErrCouldNotDetectUsername
	return
}
