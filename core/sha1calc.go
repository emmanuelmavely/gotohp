package core

import (
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"os"
	"sync/atomic"
)

const (
	contextCheckInterval = 64 * 1024 * 1024 // Check every 64MB
	copyBufferSize       = 1 * 1024 * 1024  // 1MB copy buffer
)

type chunkedContextWriter struct {
	ctx             context.Context
	w               io.Writer
	bytesSinceCheck int64
}

func (cw *chunkedContextWriter) Write(p []byte) (int, error) {
	// Check context only after accumulating enough bytes
	if cw.bytesSinceCheck >= contextCheckInterval {
		select {
		case <-cw.ctx.Done():
			return 0, cw.ctx.Err()
		default:
			cw.bytesSinceCheck = 0
		}
	}

	n, err := cw.w.Write(p)
	cw.bytesSinceCheck += int64(n)
	return n, err
}

// diskReadBytes counts every byte read from local files for uploads and
// hashing, so the GUI can chart disk throughput separately from the network.
var diskReadBytes atomic.Int64

// addDiskRead records n bytes read from local storage.
func addDiskRead(n int64) {
	if n > 0 {
		diskReadBytes.Add(n)
	}
}

// DiskReadTotal returns the cumulative bytes read from local files since the
// process started.
func DiskReadTotal() int64 { return diskReadBytes.Load() }

func CalculateSHA1(ctx context.Context, filePath string) ([]byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("error opening file: %w", err)
	}
	defer func() { _ = file.Close() }()

	hash := sha1.New()
	cw := &chunkedContextWriter{ctx: ctx, w: hash}

	// Use a large buffer (1MB) to reduce syscall overhead
	buf := make([]byte, copyBufferSize)
	n, err := io.CopyBuffer(cw, file, buf)
	if err != nil {
		return nil, fmt.Errorf("error calculating hash: %w", err)
	}
	addDiskRead(n)

	return hash.Sum(nil), nil
}
