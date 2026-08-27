package demo

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// TestDecompress checks that each container format demos ship in is unwrapped
// transparently, and that a raw demo passes through untouched.
func TestDecompress(t *testing.T) {
	payload := []byte("PBDEMS2\x00 pretend this is a demo body")

	tests := []struct {
		name string
		wrap func(*testing.T, []byte) []byte
	}{
		{"raw", func(_ *testing.T, b []byte) []byte { return b }},
		{"zstd", func(t *testing.T, b []byte) []byte {
			enc, err := zstd.NewWriter(nil)
			if err != nil {
				t.Fatal(err)
			}

			return enc.EncodeAll(b, nil)
		}},
		{"gzip", func(t *testing.T, b []byte) []byte {
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)

			if _, err := gz.Write(b); err != nil {
				t.Fatal(err)
			}

			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}

			return buf.Bytes()
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, closeStream, err := decompress(bytes.NewReader(tt.wrap(t, payload)))
			if err != nil {
				t.Fatalf("decompress: %v", err)
			}
			defer closeStream()

			got, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("reading: %v", err)
			}

			if !bytes.Equal(got, payload) {
				t.Errorf("got %q, want %q", got, payload)
			}
		})
	}
}

// TestDecompressTinyInput makes sure input shorter than the magic-byte window
// is passed through rather than treated as an error.
func TestDecompressTinyInput(t *testing.T) {
	r, closeStream, err := decompress(bytes.NewReader([]byte("ab")))
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	defer closeStream()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}

	if string(got) != "ab" {
		t.Errorf("got %q, want %q", got, "ab")
	}
}
