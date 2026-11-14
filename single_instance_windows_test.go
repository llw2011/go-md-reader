package main

import (
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestAcquireNamedMutexDetectsAlreadyExists(t *testing.T) {
	name := fmt.Sprintf(`Local\GoMarkdownReader_TestMutex_%d`, os.Getpid())

	h1, primary1 := acquireNamedMutex(name)
	if h1 == 0 || !primary1 {
		t.Fatalf("first acquireNamedMutex failed: h1=%v primary1=%v", h1, primary1)
	}
	defer windows.CloseHandle(h1)

	h2, primary2 := acquireNamedMutex(name)
	if h2 != 0 {
		defer windows.CloseHandle(h2)
	}
	if primary2 {
		t.Fatalf("expected second acquireNamedMutex on %s to report primary2=false", name)
	}
}
