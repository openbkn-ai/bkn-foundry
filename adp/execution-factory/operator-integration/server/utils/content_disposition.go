// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package utils

import (
	"mime"
	"path"
	"regexp"
	"strings"
	"unicode"
)

// defaultAttachmentStem is used when a filename has no usable characters left
// after sanitization (for example a name made only of path separators).
const defaultAttachmentStem = "download"

// attachmentIllegalChars covers path separators, Windows-reserved characters
// and the quote/backslash pair that would need escaping inside a quoted-string.
var attachmentIllegalChars = regexp.MustCompile(`[/\\:*?"<>|]+`)

// AttachmentContentDisposition builds an RFC 6266 attachment header value that
// carries the filename twice:
//
//	attachment; filename="<ascii fallback>"; filename*=UTF-8''<percent-encoded name>
//
// The extended parameter is only emitted when the sanitized filename contains
// non-ASCII characters. Browsers and HTTP clients that understand RFC 5987
// pick the UTF-8 name; everything else falls back to the ASCII one.
//
// asciiFallback is used as the plain filename when nothing printable survives
// the ASCII projection (for example a purely Chinese stem). Its extension is
// replaced by the extension of filename so the two parameters stay consistent.
func AttachmentContentDisposition(filename, asciiFallback string) string {
	name := SanitizeAttachmentFilename(filename)
	ascii := asciiProjection(name, asciiFallback)

	var b strings.Builder
	b.WriteString(`attachment; filename="`)
	b.WriteString(ascii)
	b.WriteByte('"')
	if ascii != name {
		b.WriteString("; filename*=UTF-8''")
		b.WriteString(percentEncodeRFC5987(name))
	}
	return b.String()
}

// SanitizeAttachmentFilename strips everything that must never reach a
// Content-Disposition header or a client filesystem: CR/LF and other control
// characters, path separators and platform-reserved characters. Unicode
// letters (Chinese, emoji, ...) are preserved. The result never contains a
// directory component and is never empty.
func SanitizeAttachmentFilename(filename string) string {
	if cleaned, ok := sanitizeAttachmentFilename(filename); ok {
		return cleaned
	}
	return defaultAttachmentStem
}

func sanitizeAttachmentFilename(filename string) (string, bool) {
	cleaned := strings.Map(func(r rune) rune {
		if r == unicode.ReplacementChar || unicode.IsControl(r) {
			return -1
		}
		return r
	}, filename)
	cleaned = attachmentIllegalChars.ReplaceAllString(cleaned, "_")
	cleaned = strings.Trim(strings.TrimSpace(cleaned), "._ ")
	cleaned = strings.TrimSpace(cleaned)
	return cleaned, cleaned != ""
}

// ParseContentDispositionFilename extracts the filename from a
// Content-Disposition header, preferring the RFC 5987 filename* parameter
// over the plain filename one. The returned value is sanitized so that it can
// be forwarded as-is to another attachment header or used as a local filename.
// An empty string is returned when no filename can be recovered.
func ParseContentDispositionFilename(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	raw := ""
	if _, params, err := mime.ParseMediaType(header); err == nil {
		// mime.ParseMediaType decodes filename* (RFC 2231/5987) and stores the
		// result under the bare "filename" key, overriding the plain one.
		raw = params["filename"]
	}
	if raw == "" {
		raw = legacyFilenameParam(header)
	}
	sanitized, ok := sanitizeAttachmentFilename(raw)
	if !ok {
		return ""
	}
	return sanitized
}

var legacyFilenamePattern = regexp.MustCompile(`(?i)filename\s*=\s*("([^"]*)"|([^;]*))`)

// legacyFilenameParam recovers the plain filename parameter from headers that
// mime.ParseMediaType rejects (for example raw UTF-8 inside a token value).
func legacyFilenameParam(header string) string {
	match := legacyFilenamePattern.FindStringSubmatch(header)
	if match == nil {
		return ""
	}
	if match[2] != "" {
		return match[2]
	}
	return match[3]
}

// asciiProjection reduces name to printable ASCII by dropping every other
// rune. When nothing usable survives in the stem, the fallback's stem is used
// together with name's extension so both header parameters share a suffix.
func asciiProjection(name, fallback string) string {
	ext := path.Ext(name)
	stem := trimEdges(asciiOnly(strings.TrimSuffix(name, ext)))
	if stem == "" {
		fallbackSanitized := SanitizeAttachmentFilename(fallback)
		stem = trimEdges(asciiOnly(strings.TrimSuffix(fallbackSanitized, path.Ext(fallbackSanitized))))
		if stem == "" {
			stem = defaultAttachmentStem
		}
	}
	extASCII := asciiOnly(ext)
	if extASCII == "." {
		extASCII = ""
	}
	return stem + extASCII
}

// trimEdges drops the leading/trailing spaces and dots that an ASCII
// projection can leave behind (for example " check" from "金额 check").
func trimEdges(s string) string {
	return strings.Trim(s, ". ")
}

// asciiOnly keeps printable ASCII (0x20-0x7E) minus the two characters that
// would need escaping inside a quoted-string.
func asciiOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// percentEncodeRFC5987 encodes s as an RFC 5987 value-chars sequence: every
// byte outside attr-char (ALPHA / DIGIT / "!" / "#" / "$" / "&" / "+" / "-" /
// "." / "^" / "_" / "`" / "|" / "~") is percent-encoded.
func percentEncodeRFC5987(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s) * 3)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isRFC5987AttrChar(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}

func isRFC5987AttrChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '&', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}
