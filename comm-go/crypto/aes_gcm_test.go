// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestAESGCMCipherEncrypt(t *testing.T) {
	Convey("AES-GCM encryption", t, func() {
		for _, size := range []int{16, 24, 32} {
			Convey(fmt.Sprintf("key bytes=%d", size), func() {
				key := bytes.Repeat([]byte{0x42}, size)
				storage, err := NewAESGCMCipher(key)
				So(err, ShouldBeNil)
				block, err := aes.NewCipher(key)
				So(err, ShouldBeNil)
				gcm, err := cipher.NewGCM(block)
				So(err, ShouldBeNil)

				for _, value := range []string{"", "fixture", "\x00fixture\x00", strings.Repeat("凭据-fixture-", 512)} {
					encoded, err := storage.Encrypt(value)
					So(err, ShouldBeNil)
					payload, err := base64.StdEncoding.DecodeString(encoded)
					So(err, ShouldBeNil)
					So(payload, ShouldHaveLength, 12+len(value)+gcm.Overhead())
					plain, err := gcm.Open(nil, payload[:12], payload[12:], nil)
					So(err, ShouldBeNil)
					So(string(plain), ShouldEqual, value)
					roundtrip, err := storage.Decrypt(encoded)
					So(err, ShouldBeNil)
					So(roundtrip, ShouldEqual, value)
				}
			})
		}
		Convey("each encryption has an independent nonce", func() {
			storage, err := NewAESGCMCipher(bytes.Repeat([]byte{0x42}, 32))
			So(err, ShouldBeNil)
			first, err := storage.Encrypt("same-fixture")
			So(err, ShouldBeNil)
			second, err := storage.Encrypt("same-fixture")
			So(err, ShouldBeNil)
			firstPayload, err := base64.StdEncoding.DecodeString(first)
			So(err, ShouldBeNil)
			secondPayload, err := base64.StdEncoding.DecodeString(second)
			So(err, ShouldBeNil)
			So(bytes.Equal(firstPayload[:12], secondPayload[:12]), ShouldBeFalse)
		})
		Convey("invalid keys are rejected without exposing key contents", func() {
			for _, size := range []int{0, 1, 15, 17, 23, 25, 31, 33} {
				key := bytes.Repeat([]byte{0x42}, size)
				storage, err := NewAESGCMCipher(key)
				So(err, ShouldNotBeNil)
				So(storage, ShouldBeNil)
				if size > 1 {
					So(err.Error(), ShouldNotContainSubstring, string(key))
				}
			}
		})
	})
}

func TestAESGCMCipherDecrypt(t *testing.T) {
	Convey("AES-GCM decryption", t, func() {
		key := bytes.Repeat([]byte{0x42}, 32)
		storage, err := NewAESGCMCipher(key)
		So(err, ShouldBeNil)
		block, err := aes.NewCipher(key)
		So(err, ShouldBeNil)
		gcm, err := cipher.NewGCM(block)
		So(err, ShouldBeNil)
		// A fixed nonce is used only to construct this independent format fixture.
		nonce := bytes.Repeat([]byte{0x24}, 12)
		payload := gcm.Seal(append([]byte(nil), nonce...), nonce, []byte("fixture-value"), nil)
		encoded := base64.StdEncoding.EncodeToString(payload)
		plain, err := storage.Decrypt(encoded)
		So(err, ShouldBeNil)
		So(plain, ShouldEqual, "fixture-value")

		Convey("malformed encoding and all truncated headers fail safely", func() {
			invalid := []string{"invalid-base64!", "ENC:" + encoded}
			for size := 0; size < 28; size++ {
				invalid = append(invalid, base64.StdEncoding.EncodeToString(make([]byte, size)))
			}
			for _, value := range invalid {
				plain, err := storage.Decrypt(value)
				So(err, ShouldNotBeNil)
				So(plain, ShouldBeEmpty)
				So(err.Error(), ShouldNotContainSubstring, "fixture-value")
				So(err.Error(), ShouldNotContainSubstring, "invalid-base64!")
			}
		})
		for _, position := range []int{0, 12, len(payload) - 1} {
			Convey(fmt.Sprintf("tampered byte=%d", position), func() {
				changed := append([]byte(nil), payload...)
				changed[position] ^= 1
				plain, err := storage.Decrypt(base64.StdEncoding.EncodeToString(changed))
				So(err, ShouldNotBeNil)
				So(plain, ShouldBeEmpty)
				So(err.Error(), ShouldNotContainSubstring, "fixture-value")
			})
		}
		Convey("a different key cannot authenticate the ciphertext", func() {
			other, err := NewAESGCMCipher(bytes.Repeat([]byte{0x43}, 32))
			So(err, ShouldBeNil)
			plain, err := other.Decrypt(encoded)
			So(err, ShouldNotBeNil)
			So(plain, ShouldBeEmpty)
		})
	})
}

func TestAESGCMCipherConcurrentUse(t *testing.T) {
	storage, err := NewAESGCMCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	results := make(chan error, workers)
	for worker := range workers {
		go func() {
			for iteration := range 32 {
				value := fmt.Sprintf("fixture-%d-%d", worker, iteration)
				sealed, err := storage.Encrypt(value)
				if err != nil {
					results <- err
					return
				}
				plain, err := storage.Decrypt(sealed)
				if err != nil {
					results <- err
					return
				}
				if plain != value {
					results <- errors.New("concurrent roundtrip changed the value")
					return
				}
			}
			results <- nil
		}()
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for range workers {
		select {
		case err := <-results:
			if err != nil {
				t.Error(err)
			}
		case <-timer.C:
			t.Fatal("concurrent encryption timed out")
		}
	}
}

func TestAESGCMCipherSignature(t *testing.T) {
	Convey("AES-GCM does not sign data", t, func() {
		storage, err := NewAESGCMCipher(bytes.Repeat([]byte{0x42}, 32))
		So(err, ShouldBeNil)
		signature, err := storage.Signature("fixture")
		So(err, ShouldNotBeNil)
		So(signature, ShouldBeEmpty)

	})
}
