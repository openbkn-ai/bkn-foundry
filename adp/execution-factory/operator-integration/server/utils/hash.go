// Package utils package define util in program
// @File hash.go
// @Description hash util
package utils

import (
	"crypto/md5"
	"fmt"
	"io"
	"uuid"

	jsoniter "github.com/json-iterator/go"
)

// MD5 Md5hash
func MD5(str string) string {
	h := md5.New() //nolint:gosec
	_, _ = io.WriteString(h, str)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// ObjectMD5Hash calculates the MD5 hash value of an object.
func ObjectMD5Hash(data interface{}) (string, error) {
	b, err := jsoniter.Marshal(data)
	if err != nil {
		return "", err
	}
	h := md5.New() //nolint:gosec
	_, err = h.Write(b)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// ObjectUUIDHash calculates the UUID hash value of an object.
func ObjectUUIDHash(data interface{}) (string, error) {
	b, err := jsoniter.Marshal(data)
	if err != nil {
		return "", err
	}
	digest := md5.Sum(b) //nolint:gosec // Historical object identifier.
	return uuid.UUID(digest).String(), nil
}
