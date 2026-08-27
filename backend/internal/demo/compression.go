package demo

import (
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

// Magic byte prefixes of the container formats demos ship in. FACEIT serves
// zstd, Valve match downloads are bzip2, and some sites re-wrap in gzip.
var (
	magicZstd  = []byte{0x28, 0xb5, 0x2f, 0xfd}
	magicGzip  = []byte{0x1f, 0x8b}
	magicBzip2 = []byte("BZh")
)

// decompress sniffs the first bytes of r and, if it holds a compressed stream,
// returns a reader over the decompressed demo. The returned closer releases any
// decoder resources and is never nil.
//
// An uncompressed demo is passed through untouched, so callers don't need to
// care which form they received.
func decompress(r io.Reader) (io.Reader, func(), error) {
	// Peek needs a buffer at least as large as the longest magic we compare.
	br := bufio.NewReaderSize(r, 4096)

	header, err := br.Peek(4)
	if err != nil && !isShortRead(err) {
		return nil, func() {}, fmt.Errorf("reading demo header: %w", err)
	}

	switch {
	case bytes.HasPrefix(header, magicZstd):
		// Bound the window so a hostile archive can't balloon memory.
		dec, err := zstd.NewReader(br, zstd.WithDecoderMaxWindow(1<<27)) // 128 MiB
		if err != nil {
			return nil, func() {}, fmt.Errorf("opening zstd stream: %w", err)
		}

		return dec.IOReadCloser(), dec.Close, nil

	case bytes.HasPrefix(header, magicGzip):
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, func() {}, fmt.Errorf("opening gzip stream: %w", err)
		}

		return gz, func() { _ = gz.Close() }, nil

	case bytes.HasPrefix(header, magicBzip2):
		return bzip2.NewReader(br), func() {}, nil

	default:
		return br, func() {}, nil
	}
}

// isShortRead reports whether err just means the input was smaller than the
// peek window — garbage that small is rejected by the parser further down.
func isShortRead(err error) bool {
	return errors.Is(err, io.EOF)
}
