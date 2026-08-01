/******************************************************************************
* 版权信息：中电科金仓（北京）科技股份有限公司

* 作者：KingbaseES

* 文件名：uuid.go

* 功能描述：将uuid从二进制格式转为文本格式

* 其它说明：

* 修改记录：
  1.修改时间：

  2.修改人：

  3.修改内容：

******************************************************************************/

package gokb

import (
	"encoding/hex"
	"fmt"
)

// decodeUUIDBinary解析二进制格式的uuid并以文本格式返回
func decodeUUIDBinary(src []byte) (result []byte, err error) {
	if 16 != len(src) {
		result = nil
		err = fmt.Errorf("kb: unable to decode uuid; bad length: %d", len(src))
		return
	}

	dst := make([]byte, 36)
	dst[8] = '-'
	dst[13] = '-'
	dst[18] = '-'
	dst[23] = '-'
	hex.Encode(dst[0:], src[0:4])
	hex.Encode(dst[9:], src[4:6])
	hex.Encode(dst[14:], src[6:8])
	hex.Encode(dst[19:], src[8:10])
	hex.Encode(dst[24:], src[10:16])
	result = dst
	err = nil
	return
}
