// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package crypto

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"

	"github.com/openbkn-ai/bkn-comm-go/logger"
)

type rsaCipher struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
}

func NewRSACipher(privateKeyPEM string, publicKeyPEM string) (Cipher, error) {
	ci := &rsaCipher{}

	if privateKeyPEM != "" {
		priKey, err := parsePrivateKey(privateKeyPEM)
		if err != nil {
			logger.Errorf("Failed to parse private key: %v", err)
			return nil, err
		} else {
			ci.privateKey = priKey
		}
	}

	if publicKeyPEM != "" {
		pubKey, err := parsePublicKey(publicKeyPEM)
		if err != nil {
			logger.Errorf("Failed to parse public key: %v", err)
			return nil, err
		} else {
			ci.publicKey = pubKey
		}
	}

	return ci, nil
}

// Encrypt encrypts plaintext using RSA public key with OAEP padding.
func (ci rsaCipher) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if ci.publicKey == nil {
		err := errors.New("public key not initialized")
		logger.Errorf("%v", err)
		return "", err
	}

	ciphertext, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, ci.publicKey, []byte(plaintext), nil)
	if err != nil {
		logger.Errorf("Failed to encrypt: %v", err)
		return "", err
	}

	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts ciphertext using RSA private key with OAEP padding.
func (ci rsaCipher) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	if ci.privateKey == nil {
		err := errors.New("private key not initialized")
		logger.Errorf("%v", err)
		return "", err
	}

	encrypted, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		logger.Errorf("Failed to decode base64: %v", err)
		return "", err
	}

	decrypted, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, ci.privateKey, encrypted, nil)
	if err != nil {
		logger.Errorf("Failed to decrypt: %v", err)
		return "", err
	}

	return string(decrypted), nil
}

// Signature signs content using RSA private key with PKCS1v15 and SHA-256.
func (ci rsaCipher) Signature(signContent string) (string, error) {
	if ci.privateKey == nil {
		err := errors.New("private key not initialized")
		logger.Fatalf("%v", err)
		return "", err
	}

	shaNew := sha256.New()
	shaNew.Write([]byte(signContent))
	hashed := shaNew.Sum(nil)

	signature, err := rsa.SignPKCS1v15(rand.Reader, ci.privateKey, crypto.SHA256, hashed)
	if err != nil {
		logger.Fatalf("%v", err)
		return "", err
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

func parsePrivateKey(privateKeyPEM string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return nil, errors.New("私钥信息错误！")
	}
	// Try PKCS1 first
	priKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err == nil {
		return priKey, nil
	}
	// Try PKCS8
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an RSA private key")
	}
	return rsaKey, nil
}

func parsePublicKey(publicKeyPEM string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(publicKeyPEM))
	if block == nil {
		return nil, errors.New("公钥信息错误！")
	}

	// Try PKIX format first (most common)
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err == nil {
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("not an RSA public key")
		}
		return rsaPub, nil
	}

	// Try PKCS1 format
	rsaPub, err := x509.ParsePKCS1PublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	return rsaPub, nil
}
