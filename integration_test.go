//go:build linux

package gosssd

import (
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
