// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package crypto

//go:generate mockgen -package mock -source ./cipher.go -destination ./mock/mock_cipher.go

type Cipher interface {
	Decrypt(encryptedData string) (string, error)
	Signature(signContent string) (string, error)
	Encrypt(data string) (string, error)
}
