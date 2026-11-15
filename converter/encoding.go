package converter

import (
	"bytes"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
)

// DecodeTextToUTF8 detects common text encodings (UTF-8 BOM, UTF-16 LE/BE BOM,
// valid UTF-8, and GB2312/GBK/GB18030) and normalizes text bytes to UTF-8.
func DecodeTextToUTF8(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	// 1. UTF-8 BOM
	if bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		rest := data[3:]
		if utf8.Valid(rest) {
			return string(rest)
		}
		data = rest
	}

	// 2. UTF-16 LE / BE BOM
	if len(data) >= 2 && ((data[0] == 0xFF && data[1] == 0xFE) || (data[0] == 0xFE && data[1] == 0xFF)) {
		dec := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder()
		if decoded, err := dec.Bytes(data); err == nil && utf8.Valid(decoded) {
			return string(decoded)
		}
	}

	// 3. Valid UTF-8 fast path
	if utf8.Valid(data) {
		return string(data)
	}

	// 4. Fallback for Simplified Chinese encodings (GB18030 is a superset of GB2312 and GBK).
	// Skip obvious binary payloads containing NUL bytes.
	if !bytes.ContainsRune(data, 0) {
		if decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data); err == nil && utf8.Valid(decoded) {
			return string(decoded)
		}
	}

	return string(data)
}
