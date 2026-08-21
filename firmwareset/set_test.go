package firmwareset

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestNewSetHashesSourcesAndManifestContainsNoHostPaths(t *testing.T) {
	set, err := NewSet([]Source{
		{ReaderAt: bytes.NewReader([]byte("alpha")), Size: 5},
		{ReaderAt: bytes.NewReader([]byte("beta")), Size: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	if set.Len() != 2 || set.TotalSize() != 9 {
		t.Fatalf("set size = %d pieces, %d bytes", set.Len(), set.TotalSize())
	}
	first, err := set.Piece(0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := first.SHA256(), "8ed3f6ad685b959ead7022518e1af76cd816f8e8ec7ccdda1ed4018e8f2223f8"; got != want {
		t.Fatalf("first SHA-256 = %q, want %q", got, want)
	}

	encoded, err := json.Marshal(set.Manifest())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `C:\\private`) || strings.Contains(string(encoded), "path") {
		t.Fatalf("manifest leaked a host path field: %s", encoded)
	}
}

func TestNewSetRejectsInvalidAndTruncatedSourcesWithOffsets(t *testing.T) {
	if _, err := NewSet(nil); err == nil {
		t.Fatal("NewSet accepted an empty source list")
	}
	if _, err := NewSet([]Source{{Size: 1}}); err == nil {
		t.Fatal("NewSet accepted a nil reader")
	}

	_, err := NewSet([]Source{{ReaderAt: bytes.NewReader([]byte("x")), Size: 2}})
	var readErr *ReadError
	if !errors.As(err, &readErr) {
		t.Fatalf("NewSet error = %v, want ReadError", err)
	}
	if readErr.Piece != 0 || readErr.Offset != 1 || !errors.Is(readErr, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadError = %+v", readErr)
	}
}

func TestPieceReadAtBoundsAccess(t *testing.T) {
	set, err := NewSet([]Source{{ReaderAt: bytes.NewReader([]byte("abcd")), Size: 4}})
	if err != nil {
		t.Fatal(err)
	}
	piece, err := set.Piece(0)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 2)
	if _, err := piece.ReadAt(buffer, 3); err == nil {
		t.Fatal("ReadAt accepted a range beyond the verified source")
	}
	if _, err := set.Piece(1); err == nil {
		t.Fatal("Piece accepted an out-of-range index")
	}
}
