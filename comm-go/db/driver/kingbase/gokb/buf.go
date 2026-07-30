/******************************************************************************
* 版权信息：中电科金仓（北京）科技股份有限公司

* 作者：KingbaseES

* 文件名：buf.go

* 功能描述：缓冲区操作相关的接口

* 其它说明：


* 修改记录：
  1.修改时间：

  2.修改人：

  3.修改内容：

******************************************************************************/

package gokb

import (
	"bytes"
	"encoding/binary"

	"github.com/openbkn-ai/bkn-comm-go/db/driver/kingbase/gokb/oid"
)

type writeBuf struct {
	buf []byte
	pos int
}

type readBuf []byte

func (wb *writeBuf) int32(n int) {
	x := make([]byte, 4)
	binary.BigEndian.PutUint32(x, uint32(n))
	wb.buf = append(wb.buf, x...)
}

func (rb *readBuf) int32() (n int) {
	n = int(int32(binary.BigEndian.Uint32(*rb)))
	*rb = (*rb)[4:]
	return n
}

func (rb *readBuf) oid() (n oid.Oid) {
	n = oid.Oid(binary.BigEndian.Uint32(*rb))
	*rb = (*rb)[4:]
	return n
}

func (wb *writeBuf) int16(n int) {
	x := make([]byte, 2)
	binary.BigEndian.PutUint16(x, uint16(n))
	wb.buf = append(wb.buf, x...)
}

func (rb *readBuf) int16() (n int) {
	n = int(binary.BigEndian.Uint16(*rb))
	*rb = (*rb)[2:]
	return n
}

func (wb *writeBuf) string(s string) {
	wb.buf = append(append(wb.buf, s...), '\000')
}

func (rb *readBuf) string() (s string) {
	i := bytes.IndexByte(*rb, 0)
	if 0 > i {
		errorf("invalid message format; expected string terminator")
	}
	s = string((*rb)[:i])
	*rb = (*rb)[i+1:]
	return
}

func (wb *writeBuf) byte(c byte) {
	wb.buf = append(wb.buf, c)
}

func (rb *readBuf) byte() (value byte) {
	value = rb.next(1)[0]
	return
}

func (wb *writeBuf) bytes(v []byte) {
	wb.buf = append(wb.buf, v...)
}

func (wb *writeBuf) wrap() []byte {
	p := wb.buf[wb.pos:]
	binary.BigEndian.PutUint32(p, uint32(len(p)))
	return wb.buf
}

func (wb *writeBuf) next(c byte) {
	p := wb.buf[wb.pos:]
	binary.BigEndian.PutUint32(p, uint32(len(p)))
	wb.pos = len(wb.buf) + 1
	wb.buf = append(wb.buf, c, 0, 0, 0, 0)
}

func (rb *readBuf) next(n int) []byte {
	v := (*rb)[:n]
	*rb = (*rb)[n:]
	return v
}
