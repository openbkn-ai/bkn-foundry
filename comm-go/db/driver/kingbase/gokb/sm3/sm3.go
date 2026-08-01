/*
*****************************************************************************
* 版权信息：中电科金仓（北京）科技股份有限公司

* 作者：KingbaseES

* 文件名：sm3.go

* 功能描述：

* 其它说明：

  - 修改记录：
    1.修改时间：

    2.修改人：

    3.修改内容：

*****************************************************************************
*/
package sm3

import (
	"encoding/binary"
	"hash"
)

type SM3 struct {
	digest      [8]uint32
	length      uint64 // 消息长度
	unhandleMsg []byte
}

func (sm3 *SM3) ff0(x, y, z uint32) (msg uint32) {
	msg = x ^ y ^ z
	return
}

func (sm3 *SM3) ff1(x, y, z uint32) (msg uint32) {
	msg = (x & y) | (x & z) | (y & z)
	return
}

func (sm3 *SM3) gg0(x, y, z uint32) (msg uint32) {
	msg = x ^ y ^ z
	return
}

func (sm3 *SM3) gg1(x, y, z uint32) (msg uint32) {
	msg = (x & y) | (^x & z)
	return
}

func (sm3 *SM3) p0(x uint32) (msg uint32) {
	msg = x ^ sm3.leftRotate(x, 9) ^ sm3.leftRotate(x, 17)
	return
}

func (sm3 *SM3) p1(x uint32) (msg uint32) {
	msg = x ^ sm3.leftRotate(x, 15) ^ sm3.leftRotate(x, 23)
	return
}

func (sm3 *SM3) leftRotate(x uint32, i uint32) (msg uint32) {
	msg = x<<(i%32) | x>>(32-i%32)
	return
}

func (sm3 *SM3) pad() (msg []byte) {
	msg = sm3.unhandleMsg
	msg = append(msg, 0x80)
	blockSize := 64 // 写入消息直到消息长度(以位为单位)等于448
	for 56 != len(msg)%blockSize {
		msg = append(msg, 0x00)
	}
	// append message length
	msg = append(msg, uint8(sm3.length>>56&0xff))
	msg = append(msg, uint8(sm3.length>>48&0xff))
	msg = append(msg, uint8(sm3.length>>40&0xff))
	msg = append(msg, uint8(sm3.length>>32&0xff))
	msg = append(msg, uint8(sm3.length>>24&0xff))
	msg = append(msg, uint8(sm3.length>>16&0xff))
	msg = append(msg, uint8(sm3.length>>8&0xff))
	msg = append(msg, uint8(sm3.length>>0&0xff))

	if 0 != len(msg)%64 {
		panic("------SM3 Pad: error msgLen =")
	}
	return
}

func (sm3 *SM3) update(msg []byte) {
	var w [68]uint32
	var w1 [64]uint32
	a := sm3.digest[0]
	b := sm3.digest[1]
	c := sm3.digest[2]
	d := sm3.digest[3]
	e := sm3.digest[4]
	f := sm3.digest[5]
	g := sm3.digest[6]
	h := sm3.digest[7]
	for 64 <= len(msg) {
		for i := 0; i < 16; i++ {
			w[i] = binary.BigEndian.Uint32(msg[4*i : 4*(i+1)])
		}
		for i := 16; i < 68; i++ {
			w[i] = sm3.p1(w[i-16]^w[i-9]^sm3.leftRotate(w[i-3], 15)) ^ sm3.leftRotate(w[i-13], 7) ^ w[i-6]
		}
		for i := 0; i < 64; i++ {
			w1[i] = w[i] ^ w[i+4]
		}
		A := a
		B := b
		C := c
		D := d
		E := e
		F := f
		G := g
		H := h
		for i := 0; i < 16; i++ {
			SS1 := sm3.leftRotate(sm3.leftRotate(A, 12)+E+sm3.leftRotate(0x79cc4519, uint32(i)), 7)
			SS2 := SS1 ^ sm3.leftRotate(A, 12)
			TT1, TT2 := sm3.ff0(A, B, C)+D+SS2+w1[i], sm3.gg0(E, F, G)+H+SS1+w[i]
			D = C
			C = sm3.leftRotate(B, 9)
			B = A
			A = TT1
			H = G
			G = sm3.leftRotate(F, 19)
			F = E
			E = sm3.p0(TT2)
		}
		for i := 16; i < 64; i++ {
			SS1 := sm3.leftRotate(sm3.leftRotate(A, 12)+E+sm3.leftRotate(0x7a879d8a, uint32(i)), 7)
			SS2 := SS1 ^ sm3.leftRotate(A, 12)
			TT1, TT2 := sm3.ff1(A, B, C)+D+SS2+w1[i], sm3.gg1(E, F, G)+H+SS1+w[i]
			D = C
			C = sm3.leftRotate(B, 9)
			B = A
			A = TT1
			H = G
			G = sm3.leftRotate(F, 19)
			F = E
			E = sm3.p0(TT2)
		}
		a = a ^ A
		b = b ^ B
		c = c ^ C
		d = d ^ D
		e = e ^ E
		f = f ^ F
		g = g ^ G
		h = h ^ H
		msg = msg[64:]
	}
	sm3.digest[0] = a
	sm3.digest[1] = b
	sm3.digest[2] = c
	sm3.digest[3] = d
	sm3.digest[4] = e
	sm3.digest[5] = f
	sm3.digest[6] = g
	sm3.digest[7] = h
}
func (sm3 *SM3) update2(msg []byte) [8]uint32 {
	var w [68]uint32
	var w1 [64]uint32

	a, b, c, d, e, f, g, h := sm3.digest[0], sm3.digest[1], sm3.digest[2], sm3.digest[3], sm3.digest[4], sm3.digest[5], sm3.digest[6], sm3.digest[7]
	for len(msg) >= 64 {
		for i := 0; i < 16; i++ {
			w[i] = binary.BigEndian.Uint32(msg[4*i : 4*(i+1)])
		}
		for i := 16; i < 68; i++ {
			w[i] = sm3.p1(w[i-16]^w[i-9]^sm3.leftRotate(w[i-3], 15)) ^ sm3.leftRotate(w[i-13], 7) ^ w[i-6]
		}
		for i := 0; i < 64; i++ {
			w1[i] = w[i] ^ w[i+4]
		}
		A := a
		B := b
		C := c
		D := d
		E := e
		F := f
		G := g
		H := h
		for i := 0; i < 16; i++ {
			SS1 := sm3.leftRotate(sm3.leftRotate(A, 12)+E+sm3.leftRotate(0x79cc4519, uint32(i)), 7)
			SS2 := SS1 ^ sm3.leftRotate(A, 12)
			TT1, TT2 := sm3.ff0(A, B, C)+D+SS2+w1[i], sm3.gg0(E, F, G)+H+SS1+w[i]
			D = C
			C = sm3.leftRotate(B, 9)
			B = A
			A = TT1
			H = G
			G = sm3.leftRotate(F, 19)
			F = E
			E = sm3.p0(TT2)
		}
		for i := 16; i < 64; i++ {
			SS1 := sm3.leftRotate(sm3.leftRotate(A, 12)+E+sm3.leftRotate(0x7a879d8a, uint32(i)), 7)
			SS2 := SS1 ^ sm3.leftRotate(A, 12)
			TT1, TT2 := sm3.ff1(A, B, C)+D+SS2+w1[i], sm3.gg1(E, F, G)+H+SS1+w[i]
			D = C
			C = sm3.leftRotate(B, 9)
			B = A
			A = TT1
			H = G
			G = sm3.leftRotate(F, 19)
			F = E
			E = sm3.p0(TT2)
		}
		a = a ^ A
		b = b ^ B
		c = c ^ C
		d = d ^ D
		e = e ^ E
		f = f ^ F
		g = g ^ G
		h = h ^ H
		msg = msg[64:]
	}
	var digest [8]uint32
	digest[0] = a
	digest[1] = b
	digest[2] = c
	digest[3] = d
	digest[4] = e
	digest[5] = f
	digest[6] = g
	digest[7] = h
	return digest
}

// 创建哈希计算实例
func New() (value hash.Hash) {
	var sm3 SM3
	sm3.Reset()
	value = &sm3
	return
}

// BlockSize返回哈希的底层块大小 returns the hash's underlying block size.
// 写操作必须能够接收任何大小的数据，但如果是块大小的整数倍那将会更有效率
func (sm3 *SM3) BlockSize() (size int) {
	size = 64
	return
}

// Size返回Sum将返回的字节数
func (sm3 *SM3) Size() (size int) {
	size = 32
	return
}

// Reset清除状态缓冲区
func (sm3 *SM3) Reset() {
	// Reset digest
	sm3.digest[0] = 0x7380166f
	sm3.digest[1] = 0x4914b2b9
	sm3.digest[2] = 0x172442d7
	sm3.digest[3] = 0xda8a0600
	sm3.digest[4] = 0xa96f30bc
	sm3.digest[5] = 0x163138aa
	sm3.digest[6] = 0xe38dee4d
	sm3.digest[7] = 0xb0fb0e4e

	sm3.length = 0 //重置数值状态
	sm3.unhandleMsg = []byte{}
	return
}

// Write(通过io.Writer接口)添加更多数据
// 不会返回错误
func (sm3 *SM3) Write(p []byte) (toWrite int, err error) {
	toWrite = len(p)
	sm3.length = sm3.length + uint64(len(p)*8)
	msg := append(sm3.unhandleMsg, p...)
	nblocks := len(msg) / sm3.BlockSize()
	sm3.update(msg)
	// Update unhandleMsg
	sm3.unhandleMsg = msg[nblocks*sm3.BlockSize():]
	err = nil
	return
}

// 返回SM3哈希算法摘要值
// Sum将当前哈希值添加到b并返回结果切片
// 不会改变底层哈希状态
func (sm3 *SM3) Sum(in []byte) (out []byte) {
	_, _ = sm3.Write(in)
	msg := sm3.pad()
	digest := sm3.update2(msg)

	//保存哈希值到in中
	needed := sm3.Size()
	left := cap(in) - len(in)
	if needed > left {
		newIn := make([]byte, len(in), len(in)+needed)
		copy(newIn, in)
		in = newIn
	}
	out = in[len(in) : len(in)+needed]
	for i := 0; i < 8; i++ {
		binary.BigEndian.PutUint32(out[i*4:], digest[i])
	}
	return out

}

func Sm3Sum(data []byte) (bytes []byte) {
	var sm3 SM3

	sm3.Reset()
	_, _ = sm3.Write(data)
	return sm3.Sum(nil)
}
