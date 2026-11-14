package assoc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeNotepadArgs(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantProxy bool
		wantPath  string
	}{
		{
			name:      "direct open",
			args:      []string{"go-md-reader.exe", `C:\docs\readme.md`},
			wantProxy: false,
			wantPath:  `C:\docs\readme.md`,
		},
		{
			name:      "ifeo launch without document",
			args:      []string{"go-md-reader.exe", "--notepad-proxy", `C:\Windows\System32\notepad.exe`},
			wantProxy: true,
			wantPath:  "",
		},
		{
			name:      "ifeo launch with document",
			args:      []string{"go-md-reader.exe", "--notepad-proxy", `C:\Windows\System32\notepad.exe`, `C:\notes\todo.txt`},
			wantProxy: true,
			wantPath:  `C:\notes\todo.txt`,
		},
		{
			name:      "ifeo launch with notepad flag and document",
			args:      []string{"go-md-reader.exe", "--notepad-proxy", `notepad.exe`, "/W", `C:\notes\log.txt`},
			wantProxy: true,
			wantPath:  `C:\notes\log.txt`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotProxy, gotPath := NormalizeNotepadArgs(tc.args)
			if gotProxy != tc.wantProxy || gotPath != tc.wantPath {
				t.Fatalf("NormalizeNotepadArgs(%v) = (%v, %q), want (%v, %q)", tc.args, gotProxy, gotPath, tc.wantProxy, tc.wantPath)
			}
		})
	}

	t.Run("unquoted direct open with spaces", func(t *testing.T) {
		spacedFile := filepath.Join(t.TempDir(), "jev api key.txt")
		if err := os.WriteFile(spacedFile, []byte("hello"), 0644); err != nil {
			t.Fatal(err)
		}
		splitArgs := append([]string{"go-md-reader.exe"}, strings.Split(spacedFile, " ")...)
		gotProxy, gotPath := NormalizeNotepadArgs(splitArgs)
		if gotProxy || gotPath != spacedFile {
			t.Fatalf("NormalizeNotepadArgs(%v) = (%v, %q), want (false, %q)", splitArgs, gotProxy, gotPath, spacedFile)
		}
	})
}
