package gosssd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMockServer(t *testing.T) {
	// Create temp directory for socket
	tempDir, err := os.MkdirTemp("", "gosssd-mock-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	socketPath := filepath.Join(tempDir, "nss")

	// Create and start mock server
	server, err := NewMockServer(socketPath)
	if err != nil {
		t.Fatalf("Failed to create mock server: %v", err)
	}
	defer func() { _ = server.Close() }()

	// Add test data
	server.AddUser(&User{
		Name:    "testuser",
		Passwd:  "x",
		UID:     1000,
		GID:     1000,
		Gecos:   "Test User",
		HomeDir: "/home/testuser",
		Shell:   "/bin/bash",
	})

	server.AddUser(&User{
		Name:    "alice",
		Passwd:  "x",
		UID:     1001,
		GID:     1001,
		Gecos:   "Alice Smith",
		HomeDir: "/home/alice",
		Shell:   "/bin/bash",
	})

	server.AddGroup(&Group{
		Name:    "testgroup",
		Passwd:  "x",
		GID:     2000,
		Members: []string{"testuser", "alice"},
	})

	server.AddGroup(&Group{
		Name:    "developers",
		Passwd:  "x",
		GID:     2001,
		Members: []string{"alice"},
	})

	server.SetUserGroups("testuser", []uint32{1000, 2000})
	server.SetUserGroups("alice", []uint32{1001, 2000, 2001})

	// Create client
	client := NewClient(
		WithSocketPath(socketPath),
		WithTimeout(5*time.Second),
	)

	if err := client.Connect(); err != nil {
		t.Fatalf("Failed to connect to mock server: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Test GetUserByName
	t.Run("GetUserByName", func(t *testing.T) {
		user, err := client.GetUserByName("testuser")
		if err != nil {
			t.Fatalf("GetUserByName failed: %v", err)
		}
		if user.Name != "testuser" {
			t.Errorf("Expected name 'testuser', got '%s'", user.Name)
		}
		if user.UID != 1000 {
			t.Errorf("Expected UID 1000, got %d", user.UID)
		}
		if user.GID != 1000 {
			t.Errorf("Expected GID 1000, got %d", user.GID)
		}
		if user.HomeDir != "/home/testuser" {
			t.Errorf("Expected home '/home/testuser', got '%s'", user.HomeDir)
		}
	})

	// Test GetUserByUID
	t.Run("GetUserByUID", func(t *testing.T) {
		user, err := client.GetUserByUID(1001)
		if err != nil {
			t.Fatalf("GetUserByUID failed: %v", err)
		}
		if user.Name != "alice" {
			t.Errorf("Expected name 'alice', got '%s'", user.Name)
		}
		if user.UID != 1001 {
			t.Errorf("Expected UID 1001, got %d", user.UID)
		}
	})

	// Test GetUserByName - not found
	t.Run("GetUserByName_NotFound", func(t *testing.T) {
		_, err := client.GetUserByName("nonexistent")
		if err == nil {
			t.Error("Expected error for nonexistent user")
		}
	})

	// Test GetGroupByName
	t.Run("GetGroupByName", func(t *testing.T) {
		group, err := client.GetGroupByName("testgroup")
		if err != nil {
			t.Fatalf("GetGroupByName failed: %v", err)
		}
		if group.Name != "testgroup" {
			t.Errorf("Expected name 'testgroup', got '%s'", group.Name)
		}
		if group.GID != 2000 {
			t.Errorf("Expected GID 2000, got %d", group.GID)
		}
		if len(group.Members) != 2 {
			t.Errorf("Expected 2 members, got %d", len(group.Members))
		}
	})

	// Test GetGroupByGID
	t.Run("GetGroupByGID", func(t *testing.T) {
		group, err := client.GetGroupByGID(2001)
		if err != nil {
			t.Fatalf("GetGroupByGID failed: %v", err)
		}
		if group.Name != "developers" {
			t.Errorf("Expected name 'developers', got '%s'", group.Name)
		}
		if len(group.Members) != 1 {
			t.Errorf("Expected 1 member, got %d", len(group.Members))
		}
	})

	// Test GetGroupsForUser
	t.Run("GetGroupsForUser", func(t *testing.T) {
		gids, err := client.GetGroupsForUser("alice")
		if err != nil {
			t.Fatalf("GetGroupsForUser failed: %v", err)
		}
		if len(gids) != 3 {
			t.Errorf("Expected 3 groups, got %d: %v", len(gids), gids)
		}
		// Check that all expected GIDs are present
		expected := map[uint32]bool{1001: true, 2000: true, 2001: true}
		for _, gid := range gids {
			if !expected[gid] {
				t.Errorf("Unexpected GID: %d", gid)
			}
			delete(expected, gid)
		}
		if len(expected) > 0 {
			t.Errorf("Missing GIDs: %v", expected)
		}
	})

	// Test GetGroupsForUser - empty
	t.Run("GetGroupsForUser_Empty", func(t *testing.T) {
		server.AddUser(&User{
			Name: "bob",
			UID:  1002,
			GID:  1002,
		})
		gids, err := client.GetGroupsForUser("bob")
		if err != nil {
			t.Fatalf("GetGroupsForUser failed: %v", err)
		}
		if len(gids) != 1 || gids[0] != 1002 {
			t.Errorf("Expected primary group 1002 only, got %v", gids)
		}
	})
}

func TestMockServerConcurrent(t *testing.T) {
	// Create temp directory for socket
	tempDir, err := os.MkdirTemp("", "gosssd-mock-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	socketPath := filepath.Join(tempDir, "nss")

	// Create and start mock server
	server, err := NewMockServer(socketPath)
	if err != nil {
		t.Fatalf("Failed to create mock server: %v", err)
	}
	defer func() { _ = server.Close() }()

	// Add test users
	for i := 0; i < 100; i++ {
		server.AddUser(&User{
			Name:    string(rune('a'+i%26)) + string(rune('0'+i/26)),
			UID:     uint32(1000 + i),
			GID:     uint32(1000 + i),
			HomeDir: "/home/user",
			Shell:   "/bin/bash",
		})
	}

	// Test concurrent requests
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			client := NewClient(
				WithSocketPath(socketPath),
				WithTimeout(5*time.Second),
			)
			if err := client.Connect(); err != nil {
				t.Errorf("Client %d failed to connect: %v", id, err)
				done <- false
				return
			}
			defer func() { _ = client.Close() }()

			for j := 0; j < 10; j++ {
				uid := uint32(1000 + j)
				_, err := client.GetUserByUID(uid)
				if err != nil {
					t.Errorf("Client %d GetUserByUID(%d) failed: %v", id, uid, err)
					done <- false
					return
				}
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}
