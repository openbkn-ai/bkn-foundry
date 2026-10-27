// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import (
	"bytes"
	"fmt"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

const tarBlockSize = 512

// rawTarHeader builds a ustar header block by hand. archive/tar's Writer drops
// GNU.sparse.* PAX records, so a sparse entry cannot be written with it.
func rawTarHeader(name string, typeflag byte, size int) []byte {
	block := make([]byte, tarBlockSize)
	copy(block[0:100], name)
	copy(block[100:108], "0000644\x00")
	copy(block[108:116], "0000000\x00")
	copy(block[116:124], "0000000\x00")
	copy(block[124:136], fmt.Sprintf("%011o\x00", size))
	copy(block[136:148], "00000000000\x00")
	block[156] = typeflag
	copy(block[257:263], "ustar\x00")
	copy(block[263:265], "00")
	copy(block[148:156], "        ")
	sum := 0
	for _, b := range block {
		sum += int(b)
	}
	copy(block[148:156], fmt.Sprintf("%06o\x00 ", sum))
	return block
}

func padBlock(data string) []byte {
	block := make([]byte, (len(data)+tarBlockSize-1)/tarBlockSize*tarBlockSize)
	copy(block, data)
	return block
}

// paxRecord formats "<len> key=value\n", where len counts the whole record.
func paxRecord(key string, value string) string {
	body := " " + key + "=" + value + "\n"
	n := len(body) + 1
	for len(fmt.Sprint(n))+len(body) != n {
		n++
	}
	return fmt.Sprint(n) + body
}

// sparseTar holds one PAX GNU sparse 1.0 entry: a regular file whose single
// stored byte expands to realSize bytes when read.
func sparseTar(realSize int) []byte {
	pax := paxRecord("GNU.sparse.major", "1") + paxRecord("GNU.sparse.minor", "0") +
		paxRecord("GNU.sparse.name", "big.bkn") + paxRecord("GNU.sparse.realsize", fmt.Sprint(realSize))
	sparseMap := padBlock("1\n0\n1\n") // one fragment: offset 0, length 1
	data := padBlock("x")

	var buf bytes.Buffer
	buf.Write(rawTarHeader("PaxHeaders/big.bkn", 'x', len(pax)))
	buf.Write(padBlock(pax))
	buf.Write(rawTarHeader("GNUSparseFile.0/big.bkn", '0', len(sparseMap)+1))
	buf.Write(sparseMap)
	buf.Write(data)
	buf.Write(make([]byte, 2*tarBlockSize))
	return buf.Bytes()
}

func Test_ReadBKNTar(t *testing.T) {
	Convey("Test ReadBKNTar\n", t, func() {
		Convey("A sparse entry that expands beyond the archive is refused\n", func() {
			data := sparseTar(1 << 20)
			So(len(data), ShouldBeLessThan, 1<<20)
			files, err := ReadBKNTar(data)
			So(err, ShouldNotBeNil)
			So(files, ShouldBeNil)
		})

		Convey("A sparse entry within the archive size reads back expanded\n", func() {
			files, err := ReadBKNTar(sparseTar(16))
			So(err, ShouldBeNil)
			So(string(files["big.bkn"]), ShouldEqual, "x"+string(make([]byte, 15)))
		})
	})
}
