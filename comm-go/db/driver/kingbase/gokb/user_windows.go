//go:build windows

/*
*****************************************************************************
* 版权信息：中电科金仓（北京）科技股份有限公司

* 作者：KingbaseES

* 文件名：user_windows.go

* 功能描述：

* 其它说明：

  - 修改记录：
    1.修改时间：

    2.修改人：

    3.修改内容：

*****************************************************************************
*/
package gokb

import (
	"path/filepath"
	"syscall"
)

// GetUserNameEx相比于GetUserName有更广泛的可用名称
// 为了使输出与GetUserName相同，只返回最基础(或最后的)的组件
func userCurrent() (us string, rErr error) {
	pw_name := make([]uint16, 128)
	pwname_size := uint32(len(pw_name)) - 1
	err := syscall.GetUserNameEx(syscall.NameSamCompatible, &pw_name[0], &pwname_size)
	if err != nil {
		us = ""
		rErr = ErrCouldNotDetectUsername
		return
	}
	s := syscall.UTF16ToString(pw_name)
	u := filepath.Base(s)
	us = u
	err = nil
	return
}
