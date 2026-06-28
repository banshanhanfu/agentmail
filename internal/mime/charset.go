package mime

import (
	"bytes"
	"io"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// tryDecodeNonUTF8 attempts to decode non-UTF-8 bytes as Chinese encodings.
func tryDecodeNonUTF8(s string) string {
	if s == "" || utf8.ValidString(s) {
		return ""
	}

	raw := []byte(s)

	// Try GBK (covers GB2312 too)
	if looksLikeCJK(raw) {
		decoded, err := io.ReadAll(transform.NewReader(bytes.NewReader(raw), simplifiedchinese.GBK.NewDecoder()))
		if err == nil && utf8.Valid(decoded) && len(decoded) > 0 {
			return string(decoded)
		}

		// Try GB18030 as fallback
		decoded, err = io.ReadAll(transform.NewReader(bytes.NewReader(raw), simplifiedchinese.GB18030.NewDecoder()))
		if err == nil && utf8.Valid(decoded) && len(decoded) > 0 {
			return string(decoded)
		}
	}

	return ""
}

// looksLikeCJK does a quick heuristic if bytes look like CJK multibyte encoding.
func looksLikeCJK(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	high := 0
	for _, b := range data {
		if b > 127 {
			high++
		}
	}
	// If more than 40% are high bytes, likely non-UTF8 CJK encoding
	return high > 0 && float64(high)/float64(len(data)) > 0.4
}
