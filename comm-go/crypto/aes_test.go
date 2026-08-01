// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package crypto

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

const (
	KEY   = "key"
	ODATA = "value"
	EDATA = "bDLlZGAVKoUlmFM9FEPzrQ=="
)

func TestAesDecrypt(t *testing.T) {
	Convey("test decrypt\n", t, func() {
		aesCipher := NewAESCipher(KEY)

		Convey("ECB mode test \n", func() {
			a, err := aesCipher.Decrypt(EDATA)
			So(err, ShouldBeNil)
			So(a, ShouldEqual, ODATA)
		})
	})
}

func TestAesEncrypt(t *testing.T) {
	Convey("test decrypt\n", t, func() {
		aesCipher := NewAESCipher(KEY)

		Convey("ECB mode test \n", func() {
			a, err := aesCipher.Encrypt(ODATA)
			So(err, ShouldBeNil)
			So(a, ShouldEqual, EDATA)
		})
	})
}
