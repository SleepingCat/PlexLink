package opensubtitles

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

const hashChunkSize int64 = 64 * 1024

var ErrFileTooSmall = errors.New("media file is too small for OpenSubtitles hash")

// HashError identifies a local fingerprint failure without exposing file data.
type HashError struct {
	Kind string
	Err  error
}

func (e *HashError) Error() string { return "OpenSubtitles hash: " + e.Kind }
func (e *HashError) Unwrap() error { return e.Err }

// MovieHash calculates the OpenSubtitles 64-bit movie hash. It reads only the
// first and last 64 KiB of the file.
func MovieHash(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, &HashError{Kind: "unreadable file", Err: err}
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", 0, &HashError{Kind: "cannot stat file", Err: err}
	}
	size := info.Size()
	if size < 2*hashChunkSize {
		return "", size, &HashError{Kind: "file too small", Err: ErrFileTooSmall}
	}

	sum := uint64(size)
	buf := make([]byte, hashChunkSize)
	for _, offset := range []int64{0, size - hashChunkSize} {
		if _, err := f.ReadAt(buf, offset); err != nil && !errors.Is(err, io.EOF) {
			return "", size, &HashError{Kind: "cannot read fingerprint region", Err: err}
		}
		for i := 0; i < len(buf); i += 8 {
			sum += binary.LittleEndian.Uint64(buf[i : i+8])
		}
	}

	return fmt.Sprintf("%016x", sum), size, nil
}
