package messages

import (
	"strings"
	"testing"
)

func TestNormalizeContentType(t *testing.T) {
	for in, want := range map[string]string{
		"image/png": "image/png", "IMAGE/JPEG": "image/jpeg", " text/plain; charset=utf-8 ": "text/plain", "application/pdf": "application/pdf",
		"image/svg+xml": "", "text/html": "", "application/octet-stream": "", "": "", "png": "", "image/png; broken": "",
	} {
		got, err := normalizeContentType(in)
		if (err == nil) != (want != "") || got != want {
			t.Errorf("normalizeContentType(%q) = %q, %v; queria %q", in, got, err, want)
		}
	}
}

func TestValidFilename(t *testing.T) {
	for name, ok := range map[string]bool{
		"foto.png": true, "relatório final (v2).pdf": true, "  espaços.txt  ": true, strings.Repeat("a", MaxFilenameRunes): true,
		"": false, "   ": false, ".": false, "..": false, "a/b": false, `a\b`: false, "a\nb": false, "a\x00b": false,
		strings.Repeat("a", MaxFilenameRunes+1): false, "\xff.png": false,
	} {
		if _, err := validFilename(name); (err == nil) != ok {
			t.Errorf("validFilename(%.20q) err=%v, queria ok=%v", name, err, ok)
		}
	}
}
