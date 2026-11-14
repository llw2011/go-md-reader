package dialog

import (
	"testing"
	"unicode/utf16"
)

func TestEncodeMultiSZ(t *testing.T) {
	filter := "Markdown (*.md)\x00*.md\x00All (*.*)\x00*.*\x00\x00"
	ptr, slice := encodeMultiSZ(filter)
	if ptr == nil {
		t.Fatal("expected non-nil pointer")
	}
	if len(slice) < 2 || slice[len(slice)-1] != 0 || slice[len(slice)-2] != 0 {
		t.Fatalf("expected double-NUL termination, got tail %v", slice)
	}

	decoded := string(utf16.Decode(slice))
	if decoded != filter {
		t.Fatalf("decoded filter mismatch:\nwant: %q\ngot:  %q", filter, decoded)
	}

	// Verify helper also appends double NUL when input omits trailing NULs
	_, padded := encodeMultiSZ("Label\x00*.txt")
	if len(padded) < 2 || padded[len(padded)-1] != 0 || padded[len(padded)-2] != 0 {
		t.Fatalf("expected helper to ensure double-NUL padding")
	}
}
