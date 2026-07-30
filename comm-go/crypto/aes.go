// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-comm-go/logger"
)

const (
	DEFAULT_CIPHER_MODE = "ECB"
)

type aesCipher struct {
	key        string
	cipherMode string
}

func NewAESCipher(key string) Cipher {
	ci := &aesCipher{
		key:        key,
		cipherMode: DEFAULT_CIPHER_MODE,
	}
	return ci
}

func (ci aesCipher) Decrypt(encryptedData string) (string, error) {
	switch ci.cipherMode {
	case "CBC":
		return ci.decryptCBC(encryptedData)
	case "ECB":
		return ci.decryptECB(encryptedData)
	default:
		err := fmt.Errorf("invalid AES Cipher Mode: %s", ci.cipherMode)
		logger.Fatalf("%v", err)
		return "", err
	}
}

func (ci aesCipher) Encrypt(encryptedData string) (string, error) {
	switch ci.cipherMode {
	case "ECB":
		return ci.encryptECB(encryptedData)
	default:
		err := fmt.Errorf("invalid AES Cipher Mode: %s", ci.cipherMode)
		logger.Fatalf("%v", err)
		return "", err
	}
}

// CBC方式解密
func (ci aesCipher) decryptCBC(encryptedData string) (string, error) {
	encrypted, err := base64.StdEncoding.DecodeString(encryptedData)
	if err != nil {
		return "", err
	}
	key := []byte(ci.key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	blockSize := block.BlockSize()
	blockMode := cipher.NewCBCDecrypter(block, key[:blockSize])
	decrypted := make([]byte, len(encrypted))
	blockMode.CryptBlocks(decrypted, encrypted)
	decryptedData := ci.pkcs5UnPadding(decrypted)
	return string(decryptedData), nil
}

// pkcs5方式解除填充
func (ci aesCipher) pkcs5UnPadding(decryptedData []byte) []byte {
	length := len(decryptedData)
	unpadding := int(decryptedData[length-1])
	return decryptedData[:(length - unpadding)]
}

// ECB方式加密
func (ci aesCipher) encryptECB(data string) (string, error) {
	cipherText := []byte(data)
	key := []byte(ci.key)
	block, err := aes.NewCipher(ci.generateKey(key))
	if err != nil {
		return "", err
	}
	blockSize := block.BlockSize()

	length := (len(cipherText) + aes.BlockSize) / aes.BlockSize
	plain := make([]byte, length*aes.BlockSize)
	copy(plain, cipherText)

	encrypted := make([]byte, len(plain))
	for bs, be := 0, blockSize; bs <= len(cipherText); bs, be = bs+blockSize, be+blockSize {
		block.Encrypt(encrypted[bs:be], plain[bs:be])
	}

	b := base64.StdEncoding.EncodeToString(encrypted)
	return b, nil
}

// ECB方式解密
func (ci aesCipher) decryptECB(encryptedData string) (string, error) {
	encrypted, err := base64.StdEncoding.DecodeString(encryptedData)
	if err != nil {
		return "", err
	}
	key := []byte(ci.key)
	block, err := aes.NewCipher(ci.generateKey(key))
	if err != nil {
		return "", err
	}
	blockSize := block.BlockSize()
	decrypted := make([]byte, len(encrypted))
	for bs, be := 0, blockSize; bs < len(encrypted); bs, be = bs+blockSize, be+blockSize {
		block.Decrypt(decrypted[bs:be], encrypted[bs:be])
	}

	trim := 0
	if len(decrypted) > 0 {
		trim = len(decrypted) - int(decrypted[len(decrypted)-1])
	}

	decryptedData := strings.TrimRight(string(decrypted[:trim]), "\x00")
	return decryptedData, nil
}

func (ci aesCipher) generateKey(key []byte) (genKey []byte) {
	genKey = make([]byte, 16)
	copy(genKey, key)
	for i := 16; i < len(key); {
		for j := 0; j < 16 && i < len(key); j, i = j+1, i+1 {
			genKey[j] ^= key[i]
		}
	}
	return genKey
}

func (ci aesCipher) Signature(signContent string) (string, error) {
	err := errors.New("aesCipher Signature is Not implemented Yet")
	logger.Fatalf("%v", err)
	return "", err
}
