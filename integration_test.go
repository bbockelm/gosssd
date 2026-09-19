//go:build linux

package gosssd

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func TestIntegrationGetUserByName(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ts := SetupTestSSSD(t)
	if ts == nil {
		return // sssd not available, test was skipped
	}
	defer ts.Cleanup()

	// Create client
	client := NewClient(
		WithSocketPath(ts.SocketPath),
		WithTimeout(5*time.Second),
	)

	if err := client.Connect(); err != nil {
		t.Fatalf("Failed to connect to test SSSD: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Test getting user by name
	user, err := client.GetUserByName(ts.TestUser.Username)
	if err != nil {
		t.Fatalf("GetUserByName failed: %v", err)
	}

	expectedUID, _ := strconv.ParseUint(ts.TestUser.Uid, 10, 32)
	expectedGID, _ := strconv.ParseUint(ts.TestUser.Gid, 10, 32)

	if user.Name != ts.TestUser.Username {
		t.Errorf("Expected username '%s', got '%s'", ts.TestUser.Username, user.Name)
	}
	if user.UID != uint32(expectedUID) {
		t.Errorf("Expected UID %d, got %d", expectedUID, user.UID)
	}
	if user.GID != uint32(expectedGID) {
		t.Errorf("Expected GID %d, got %d", expectedGID, user.GID)
	}
}

func TestIntegrationGetUserByUID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ts := SetupTestSSSD(t)
	if ts == nil {
		return
	}
	defer ts.Cleanup()

	client := NewClient(
		WithSocketPath(ts.SocketPath),
		WithTimeout(5*time.Second),
	)

	if err := client.Connect(); err != nil {
		t.Fatalf("Failed to connect to test SSSD: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Test getting user by UID (use the discovered test user)
	expectedUID, _ := strconv.ParseUint(ts.TestUser.Uid, 10, 32)
	user, err := client.GetUserByUID(uint32(expectedUID))
	if err != nil {
		t.Fatalf("GetUserByUID failed: %v", err)
	}

	expectedGID, _ := strconv.ParseUint(ts.TestUser.Gid, 10, 32)

	if user.Name != ts.TestUser.Username {
		t.Errorf("Expected username '%s', got '%s'", ts.TestUser.Username, user.Name)
	}
	if user.UID != uint32(expectedUID) {
		t.Errorf("Expected UID %d, got %d", expectedUID, user.UID)
	}
	if user.GID != uint32(expectedGID) {
		t.Errorf("Expected GID %d, got %d", expectedGID, user.GID)
	}
}

func TestIntegrationGetGroupByName(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ts := SetupTestSSSD(t)
	if ts == nil {
		return
	}
	defer ts.Cleanup()

	client := NewClient(
		WithSocketPath(ts.SocketPath),
		WithTimeout(5*time.Second),
	)

	if err := client.Connect(); err != nil {
		t.Fatalf("Failed to connect to test SSSD: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Test getting group by name (use discovered test group)
	group, err := client.GetGroupByName(ts.TestGroup.Name)
	if err != nil {
		t.Fatalf("GetGroupByName failed: %v", err)
	}

	expectedGID, _ := strconv.ParseUint(ts.TestGroup.Gid, 10, 32)

	if group.Name != ts.TestGroup.Name {
		t.Errorf("Expected group name '%s', got '%s'", ts.TestGroup.Name, group.Name)
	}
	if group.GID != uint32(expectedGID) {
		t.Errorf("Expected GID %d, got %d", expectedGID, group.GID)
	}
	t.Logf("Group %s has %d members", group.Name, len(group.Members))
}

func TestIntegrationGetGroupByGID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ts := SetupTestSSSD(t)
	if ts == nil {
		return
	}
	defer ts.Cleanup()

	client := NewClient(
		WithSocketPath(ts.SocketPath),
		WithTimeout(5*time.Second),
	)

	if err := client.Connect(); err != nil {
		t.Fatalf("Failed to connect to test SSSD: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Test getting group by GID (use discovered test group)
	expectedGID, _ := strconv.ParseUint(ts.TestGroup.Gid, 10, 32)
	group, err := client.GetGroupByGID(uint32(expectedGID))
	if err != nil {
		t.Fatalf("GetGroupByGID failed: %v", err)
	}

	if group.Name != ts.TestGroup.Name {
		t.Errorf("Expected group name '%s', got '%s'", ts.TestGroup.Name, group.Name)
	}
	if group.GID != uint32(expectedGID) {
		t.Errorf("Expected GID %d, got %d", expectedGID, group.GID)
	}
}

func TestIntegrationGetGroupsForUser(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ts := SetupTestSSSD(t)
	if ts == nil {
		return
	}
	defer ts.Cleanup()

	client := NewClient(
		WithSocketPath(ts.SocketPath),
		WithTimeout(5*time.Second),
	)

	if err := client.Connect(); err != nil {
		t.Fatalf("Failed to connect to test SSSD: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Test getting groups for user (use discovered test user)
	gids, err := client.GetGroupsForUser(ts.TestUser.Username)
	if err != nil {
		t.Fatalf("GetGroupsForUser failed: %v", err)
	}

	// Should include at least the primary group
	expectedGID, _ := strconv.ParseUint(ts.TestUser.Gid, 10, 32)
	found := false
	for _, gid := range gids {
		if gid == uint32(expectedGID) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected primary group %d in list, got: %v", expectedGID, gids)
	}
	t.Logf("User %s is in groups: %v", ts.TestUser.Username, gids)
}

// TestIntegrationConnectContextThenGetGroups covers ConnectContext
// against a real SSSD.
//
// Every other integration test here connects with Connect(), which was
// always correct. ConnectContext dialed a shallow copy of the client and
// stored the socket on that copy, so it reported success while leaving
// the caller's client unconnected and every later request failing with
// "not connected". The harness was pointed exclusively at the working
// path, so a real SSSD, a real group lookup, and a passing CI run all
// coexisted with a client that could not be used this way at all.
//
// This is deliberately the same assertion as
// TestIntegrationGetGroupsForUser, differing only in how the connection
// is established -- which is the whole of what went wrong.
func TestIntegrationConnectContextThenGetGroups(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ts := SetupTestSSSD(t)
	if ts == nil {
		return
	}
	defer ts.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := NewClient(
		WithSocketPath(ts.SocketPath),
		WithTimeout(5*time.Second),
	)

	if err := client.ConnectContext(ctx); err != nil {
		t.Fatalf("ConnectContext failed: %v", err)
	}
	defer func() { _ = client.Close() }()

	gids, err := client.GetGroupsForUser(ts.TestUser.Username)
	if err != nil {
		// The pre-fix failure mode, named so a regression is unambiguous.
		t.Fatalf("GetGroupsForUser after ConnectContext failed: %v "+
			"(a \"not connected\" error here means ConnectContext left the client unconnected)", err)
	}

	expectedGID, _ := strconv.ParseUint(ts.TestUser.Gid, 10, 32)
	found := false
	for _, gid := range gids {
		if gid == uint32(expectedGID) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected primary group %d in list, got: %v", expectedGID, gids)
	}
	t.Logf("ConnectContext: user %s is in groups: %v", ts.TestUser.Username, gids)
}

// Enumeration is the one call whose answer depends on the domain's
// `enumerate` setting, so it is worth exercising against a real SSSD
// rather than only a mock: the mock cannot tell us whether the
// SETPWENT/GETPWENT/ENDPWENT exchange is the one SSSD expects.
func TestIntegrationEnumerateUsers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ts := SetupTestSSSD(t)
	if ts == nil {
		return // sssd not available, test was skipped
	}
	defer ts.Cleanup()

	client := NewClient(
		WithSocketPath(ts.SocketPath),
		WithTimeout(10*time.Second),
	)
	if err := client.Connect(); err != nil {
		t.Fatalf("Failed to connect to test SSSD: %v", err)
	}
	defer func() { _ = client.Close() }()

	users, err := client.EnumerateUsers()
	if err != nil {
		t.Fatalf("EnumerateUsers failed: %v", err)
	}
	if len(users) == 0 {
		t.Fatal("enumeration returned nobody; the test domain sets enumerate = true")
	}

	// The account the harness created must be among them, with the same
	// fields a by-name lookup reports -- enumeration that returned
	// differently-shaped entries would be worse than none.
	byName, err := client.GetUserByName(ts.TestUser.Username)
	if err != nil {
		t.Fatalf("GetUserByName failed: %v", err)
	}

	var found *User
	for _, u := range users {
		if u.Name == ts.TestUser.Username {
			found = u
			break
		}
	}
	if found == nil {
		names := make([]string, 0, len(users))
		for _, u := range users {
			names = append(names, u.Name)
		}
		t.Fatalf("enumeration did not include %q; got %v", ts.TestUser.Username, names)
	}
	if found.UID != byName.UID || found.GID != byName.GID {
		t.Errorf("enumerated %s as uid=%d gid=%d, by-name says uid=%d gid=%d",
			found.Name, found.UID, found.GID, byName.UID, byName.GID)
	}
	if found.Gecos != byName.Gecos || found.HomeDir != byName.HomeDir || found.Shell != byName.Shell {
		t.Errorf("enumerated entry differs from the by-name lookup:\n  enum: %+v\n  name: %+v", found, byName)
	}

	// Enumeration keeps per-connection state in SSSD. A second pass on the
	// same client must start from the beginning, not resume where the first
	// stopped -- that is what ENDPWENT is for.
	again, err := client.EnumerateUsers()
	if err != nil {
		t.Fatalf("second EnumerateUsers failed: %v", err)
	}
	if len(again) != len(users) {
		t.Errorf("second enumeration returned %d users, first returned %d; the cursor was not reset",
			len(again), len(users))
	}
}
