package pkg_test

import (
	"oss-gateway/pkg/crypto"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AES-256 requires a 32-byte key; NewAESCrypto returns nil for anything else.
const (
	testKey    = "01234567890123456789012345678901"
	testAltKey = "abcdefghijklmnopqrstuvwxyz123456"
)

func TestNewAESCrypto_ValidKey(t *testing.T) {
	aes, err := crypto.NewAESCrypto(testKey)

	assert.NoError(t, err)
	assert.NotNil(t, aes)
}

func TestNewAESCrypto_InvalidKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{
			name: "Key too short",
			key:  "short",
		},
		{
			name: "Key too long",
			key:  "123456789012345678901234567890123",
		},
		{
			name: "Empty key",
			key:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			aes, err := crypto.NewAESCrypto(tt.key)
			assert.Error(t, err)
			assert.Nil(t, aes)
		})
	}
}

func TestAESCrypto_Encrypt_Success(t *testing.T) {
	aes, err := crypto.NewAESCrypto(testKey)
	require.NoError(t, err)

	plaintext := "Hello, World!"
	ciphertext, err := aes.Encrypt(plaintext)

	assert.NoError(t, err)
	assert.NotEmpty(t, ciphertext)
	assert.NotEqual(t, plaintext, ciphertext)
}

func TestAESCrypto_Decrypt_Success(t *testing.T) {
	aes, err := crypto.NewAESCrypto(testKey)
	require.NoError(t, err)

	plaintext := "Hello, World!"
	ciphertext, _ := aes.Encrypt(plaintext)

	decrypted, err := aes.Decrypt(ciphertext)

	assert.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestAESCrypto_EncryptDecrypt_MultipleValues(t *testing.T) {
	aes, err := crypto.NewAESCrypto(testKey)
	require.NoError(t, err)

	testCases := []string{
		"",
		"a",
		"short text",
		"This is a longer text with special characters: !@#$%^&*()",
		"中文测试",
		"12345678901234567890123456789012345678901234567890",
	}

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			encrypted, err := aes.Encrypt(tc)
			assert.NoError(t, err)

			decrypted, err := aes.Decrypt(encrypted)
			assert.NoError(t, err)
			assert.Equal(t, tc, decrypted)
		})
	}
}

func TestAESCrypto_Decrypt_InvalidCiphertext(t *testing.T) {
	aes, err := crypto.NewAESCrypto(testKey)
	require.NoError(t, err)

	tests := []struct {
		name       string
		ciphertext string
	}{
		{
			name:       "Invalid base64",
			ciphertext: "not-valid-base64!@#",
		},
		{
			name:       "Too short",
			ciphertext: "YWJj", // "abc" in base64, too short for AES
		},
		{
			name:       "Empty string",
			ciphertext: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := aes.Decrypt(tt.ciphertext)
			assert.Error(t, err)
		})
	}
}

func TestAESCrypto_EncryptProducesDifferentOutputs(t *testing.T) {
	aes, err := crypto.NewAESCrypto(testKey)
	require.NoError(t, err)

	plaintext := "same plaintext"

	encrypted1, _ := aes.Encrypt(plaintext)
	encrypted2, _ := aes.Encrypt(plaintext)

	// Encryption uses a random IV, so two encryptions should differ.
	assert.NotEqual(t, encrypted1, encrypted2)

	// Both ciphertexts should still decrypt correctly.
	decrypted1, _ := aes.Decrypt(encrypted1)
	decrypted2, _ := aes.Decrypt(encrypted2)

	assert.Equal(t, plaintext, decrypted1)
	assert.Equal(t, plaintext, decrypted2)
}

func TestAESCrypto_DifferentKeysProduceDifferentOutputs(t *testing.T) {
	aes1, err := crypto.NewAESCrypto(testKey)
	require.NoError(t, err)
	aes2, err := crypto.NewAESCrypto(testAltKey)
	require.NoError(t, err)

	plaintext := "test message"

	encrypted1, _ := aes1.Encrypt(plaintext)
	encrypted2, _ := aes2.Encrypt(plaintext)

	// Different keys should produce different ciphertext.
	assert.NotEqual(t, encrypted1, encrypted2)

	// Decryption with the wrong key should fail or produce an invalid result.
	_, err = aes1.Decrypt(encrypted2)
	// It may fail immediately or decrypt to the wrong plaintext.
	if err == nil {
		decrypted, _ := aes1.Decrypt(encrypted2)
		assert.NotEqual(t, plaintext, decrypted)
	}
}

func TestAESCrypto_LargeData(t *testing.T) {
	aes, err := crypto.NewAESCrypto(testKey)
	require.NoError(t, err)

	// Generate a large string.
	largeText := ""
	for i := 0; i < 10000; i++ {
		largeText += "a"
	}

	encrypted, err := aes.Encrypt(largeText)
	assert.NoError(t, err)

	decrypted, err := aes.Decrypt(encrypted)
	assert.NoError(t, err)
	assert.Equal(t, largeText, decrypted)
}
