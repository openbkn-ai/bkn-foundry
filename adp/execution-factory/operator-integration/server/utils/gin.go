package utils

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
)

// BindPreciseJSON preserves numbers in dynamic request fields without changing Gin's global decoder.
func BindPreciseJSON(c *gin.Context, target any) error {
	if c.Request.Body == nil {
		return io.EOF
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return binding.Validator.ValidateStruct(target)
}

// GetBindJSONRaw gets the original request body.
func GetBindJSONRaw(c *gin.Context, req interface{}) (err error) {
	// Before reading the request body, check whether it is empty.
	if c.Request.Body == nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, "request body is empty")
		return
	}
	// Read request body.
	err = c.ShouldBindJSON(req)
	if err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
	}
	return
}

// GetBindMultipartFormRaw Gets the original multipart/form-data request body.
func GetBindMultipartFormRaw(c *gin.Context, req interface{}, fileKey string, fileSizeLimit int64) (fileBytes []byte, err error) {
	// Before reading the request body, check whether it is empty.
	if c.Request.Body == nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, "request body is empty")
		return
	}
	// Read request body.
	err = c.ShouldBindWith(req, binding.Form)
	if err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		return
	}
	// Get file.
	var file *multipart.FileHeader
	file, err = c.FormFile(fileKey)
	if err != nil {
		// Determine whether the file does not exist error.
		if err == http.ErrMissingFile || err.Error() == "http: no such file" {
			err = nil
		} else {
			err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		}
		return
	}
	// TODO: Check file size.
	if fileSizeLimit > 0 && file.Size > fileSizeLimit {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, "file size exceeds limit")
		return
	}
	var fileContent multipart.File
	fileContent, err = file.Open()
	if err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		return
	}
	defer func() {
		_ = fileContent.Close()
	}()
	// Read file contents.
	buf := new(bytes.Buffer)
	if _, err = buf.ReadFrom(fileContent); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		return
	}
	fileBytes = buf.Bytes()
	return
}

// GetBindFormRaw gets the original application/x-www-form-urlencoded request body.
func GetBindFormRaw(c *gin.Context, req interface{}) (err error) {
	// Before reading the request body, check whether it is empty.
	if c.Request.Body == nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, "request body is empty")
		return
	}
	// Read request body.
	err = c.ShouldBind(req)
	if err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
	}
	return
}
