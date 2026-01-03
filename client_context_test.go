package gosssd

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClientContextDeadlineClosesConnection(t *testing.T) {
	tempDir := t.TempDir()
	socketPath := filepath.Join(tempDir, "nss.sock")

	// macOS has a short AF_UNIX path limit; fall back to /tmp if needed.
	if len(socketPath) >= 100 { // leave headroom under the ~104 byte limit on darwin
		shortDir, err := os.MkdirTemp("/tmp", "gosssd-uds-")
		if err != nil {
			t.Fatalf("failed to create short temp dir: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(shortDir) })
		socketPath = filepath.Join(shortDir, "nss.sock")
	}

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	client := NewClient(
		WithSocketPath(socketPath),
		WithTimeout(2*time.Second),
		WithContext(ctx),
	)

	// Accept connection and hold it open without replying so reads block until the context closes the socket.
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = io.Copy(io.Discard, conn)
	}()

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer func() { _ = client.Close() }()

	_, err = client.GetUserByName("nobody")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}

	// Surface any accept errors that happened before cancellation triggered.
	select {
	case err := <-acceptErr:
		t.Fatalf("accept failed: %v", err)
	default:
	}
}
