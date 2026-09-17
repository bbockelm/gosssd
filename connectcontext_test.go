package gosssd

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// listenStubSSSD opens a unix socket that accepts and then does nothing,
// which is all Connect needs to succeed.
func listenStubSSSD(t *testing.T) string {
	t.Helper()
	// The socket path must stay under the 108-byte sun_path limit.
	path := filepath.Join(t.TempDir(), "s")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Skipf("cannot listen on a unix socket here: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	return path
}

// ConnectContext must leave the CLIENT connected, not a throwaway copy of
// it. It used to dial on a shallow clone, so the caller's client kept a
// nil conn and every subsequent request failed with "not connected" --
// while ConnectContext itself had reported success.
func TestConnectContextConnectsTheClient(t *testing.T) {
	c := NewClient(WithSocketPath(listenStubSSSD(t)), WithTimeout(2*time.Second))
	defer func() { _ = c.Close() }()

	if err := c.ConnectContext(context.Background()); err != nil {
		t.Fatalf("ConnectContext: %v", err)
	}

	c.mu.Lock()
	connected := c.conn != nil
	c.mu.Unlock()
	if !connected {
		t.Fatal("ConnectContext reported success but the client is not connected")
	}
}

// The observable version of the same bug: a request made after a
// successful ConnectContext must not fail with "not connected".
func TestRequestAfterConnectContext(t *testing.T) {
	c := NewClient(WithSocketPath(listenStubSSSD(t)), WithTimeout(500*time.Millisecond))
	defer func() { _ = c.Close() }()

	if err := c.ConnectContext(context.Background()); err != nil {
		t.Fatalf("ConnectContext: %v", err)
	}

	// The stub never replies, so this must fail on a read/timeout -- but
	// never because the client was never connected in the first place.
	_, err := c.GetGroupsForUser("anyone")
	if err == nil {
		t.Skip("stub unexpectedly satisfied the request")
	}
	if got := err.Error(); strings.Contains(got, "not connected") {
		t.Fatalf("request after a successful ConnectContext failed with %q", got)
	}
}
