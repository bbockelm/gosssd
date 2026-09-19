package gosssd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// socketPath returns a path short enough to bind: the sockaddr_un limit is
// about 104 bytes on darwin, and t.TempDir() alone overruns it.
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "gosssd-uds-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "nss")
}

func mockAt(t *testing.T, path string) *MockServer {
	t.Helper()
	srv, err := NewMockServer(path)
	if err != nil {
		t.Fatalf("starting mock SSSD: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func sampleUser(name string, uid uint32) *User {
	return &User{
		Name: name, Passwd: "x", UID: uid, GID: uid,
		Gecos: name + "-gecos", HomeDir: "/home/" + name, Shell: "/bin/sh",
	}
}

// A client built before SSSD is listening must not be permanently useless.
// In a container the daemon and its SSSD sidecar start together, so this is
// the ordinary case rather than an edge one.
func TestClientWorksWhenTheSocketAppearsLater(t *testing.T) {
	path := socketPath(t)
	c := NewClient(WithSocketPath(path))

	// Nothing is listening yet: connecting fails, as it should.
	if err := c.Connect(); err == nil {
		t.Fatal("connecting to a socket that does not exist succeeded")
	}

	// SSSD comes up.
	srv := mockAt(t, path)
	srv.AddUser(sampleUser("bbockelm", 20014))

	u, err := c.GetUserByName("bbockelm")
	if err != nil {
		t.Fatalf("the client never recovered once the socket appeared: %v", err)
	}
	if u.Gecos != "bbockelm-gecos" {
		t.Errorf("Gecos = %q", u.Gecos)
	}
}

// A restarted SSSD leaves the cached connection dead. Before this the
// client kept it and every later request failed with "not connected".
func TestStatelessLookupRedialsAfterTheConnectionDies(t *testing.T) {
	path := socketPath(t)
	srv := mockAt(t, path)
	srv.AddUser(sampleUser("bbockelm", 20014))

	c := NewClient(WithSocketPath(path), WithRetry(2, 10*time.Millisecond))
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// The next request is met with a hang-up, exactly as it would be if
	// SSSD had been restarted underneath this connection.
	srv.DropConnectionAfter(1)

	u, err := c.GetUserByName("bbockelm")
	if err != nil {
		t.Fatalf("the client did not recover from a dropped connection: %v", err)
	}
	if u.Name != "bbockelm" {
		t.Errorf("Name = %q", u.Name)
	}
}

// "No such user" is SSSD's answer, not a broken link. Retrying it would
// triple the cost of every negative lookup and change nothing.
func TestAnAnswerFromSSSDIsNotRetried(t *testing.T) {
	path := socketPath(t)
	srv := mockAt(t, path)
	srv.AddUser(sampleUser("bbockelm", 20014))

	c := NewClient(WithSocketPath(path), WithRetry(2, 10*time.Millisecond))
	if _, err := c.GetUserByName("nobody-here"); err == nil {
		t.Fatal("a missing user was reported as found")
	}
	if got := srv.Requests(); got != 1 {
		t.Errorf("server saw %d requests for one negative lookup; an answer was retried as if it were a failure", got)
	}
}

// The enumeration cursor lives on the connection. If a reconnect resumed
// mid-sequence instead of starting over, the result would silently contain
// duplicates -- a wrong answer returned as a successful one. Here the link
// dies after the batch has been delivered but before the terminating empty
// batch, which is exactly the window where a resume would double the list.
func TestEnumerationRestartsRatherThanResumingAfterAReconnect(t *testing.T) {
	path := socketPath(t)
	srv := mockAt(t, path)
	srv.AddUser(sampleUser("alice", 1001))
	srv.AddUser(sampleUser("bob", 1002))
	srv.AddUser(sampleUser("carol", 1003))

	c := NewClient(WithSocketPath(path), WithRetry(2, 10*time.Millisecond))

	// SETPWENT, then the first GETPWENT is answered, then the third
	// request gets a hang-up.
	srv.DropConnectionAfter(3)

	users, err := c.EnumerateUsers()
	if err != nil {
		t.Fatalf("EnumerateUsers did not recover: %v", err)
	}

	seen := map[string]int{}
	for _, u := range users {
		seen[u.Name]++
	}
	if len(users) != 3 {
		t.Errorf("got %d users, want 3: %v", len(users), seen)
	}
	for _, name := range []string{"alice", "bob", "carol"} {
		if seen[name] != 1 {
			t.Errorf("%s appeared %d times, want exactly once", name, seen[name])
		}
	}
}

// Retries are opt-outable, so a caller that wants a single attempt still
// gets one.
func TestRetryCanBeDisabled(t *testing.T) {
	path := socketPath(t)
	srv := mockAt(t, path)
	srv.AddUser(sampleUser("bbockelm", 20014))

	c := NewClient(WithSocketPath(path), WithRetry(0, 0))
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	srv.DropConnectionAfter(1)

	if _, err := c.GetUserByName("bbockelm"); err == nil {
		t.Fatal("WithRetry(0, 0) still retried")
	}
}
