//go:build linux

package gosssd

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"testing"
	"time"
)

// TestSSSD manages a self-contained SSSD instance for testing
type TestSSSD struct {
	TempDir    string
	SocketsDir string
	SocketPath string
	Cmd        *exec.Cmd
	t          *testing.T
	// Test users/groups discovered from the system
	TestUser  *user.User
	TestUser2 *user.User
	TestGroup *user.Group
}

// SetupTestSSSD creates and starts a self-contained SSSD instance for testing.
// Returns nil if sssd is not available (skips test gracefully).
func SetupTestSSSD(t *testing.T) *TestSSSD {
	t.Helper()

	// Build custom SSSD with custom paths
	sssdInstallDir, err := BuildCustomSSSD(t)
	if err != nil {
		t.Skipf("Failed to build custom SSSD: %v", err)
		return nil
	}

	// Create temporary directory for this test instance
	tempDir, err := os.MkdirTemp("", "gosssd-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Create necessary subdirectories in the SSSD install dir
	dirs := []string{
		filepath.Join(sssdInstallDir, "db"),
		filepath.Join(sssdInstallDir, "run"),
		filepath.Join(sssdInstallDir, "log"),
		filepath.Join(sssdInstallDir, "pipes"),
		filepath.Join(sssdInstallDir, "etc"),
	}
	for _, dir := range dirs {
		_ = os.MkdirAll(dir, 0755)
	}

	// Create a temporary directory for sockets (to work around filesystem limitations)
	socketsDir, err := os.MkdirTemp("", "gosssd-sockets-*")
	if err != nil {
		_ = os.RemoveAll(tempDir)
		t.Fatalf("Failed to create sockets temp dir: %v", err)
	}
	_ = os.Chmod(socketsDir, 0700)

	// Symlink pipes/private to the temp directory to avoid chmod issues on sockets
	privatePipesPath := filepath.Join(sssdInstallDir, "pipes", "private")
	_ = os.RemoveAll(privatePipesPath) // Remove if it exists
	if err := os.Symlink(socketsDir, privatePipesPath); err != nil {
		_ = os.RemoveAll(tempDir)
		_ = os.RemoveAll(socketsDir)
		t.Fatalf("Failed to create symlink for pipes/private: %v", err)
	}

	// Create sssd.conf
	confPath := filepath.Join(tempDir, "sssd.conf")
	sssdConf := `[sssd]
services = nss
domains = LOCAL
config_file_version = 2
user = root

[nss]
memcache_timeout = 0

[domain/LOCAL]
id_provider = proxy
proxy_lib_name = files
proxy_pam_target = sssd-shadowutils
enumerate = true
cache_credentials = false
`

	if err := os.WriteFile(confPath, []byte(sssdConf), 0600); err != nil {
		_ = os.RemoveAll(tempDir)
		t.Fatalf("Failed to write sssd.conf: %v", err)
	}

	// Probe for existing test users - try common test users, container users, or fall back to well-known users
	var testUser, testUser2 *user.User
	var testGroup *user.Group

	// Try to find suitable test users (prefer non-root users for testing)
	candidateUsers := []string{"vscode", "node", "nobody", "daemon", "bin"}
	for _, username := range candidateUsers {
		if u, err := user.Lookup(username); err == nil {
			if testUser == nil {
				testUser = u
				t.Logf("Using test user: %s (UID %s)", testUser.Username, testUser.Uid)
			} else if testUser2 == nil && u.Uid != testUser.Uid {
				testUser2 = u
				t.Logf("Using second test user: %s (UID %s)", testUser2.Username, testUser2.Uid)
				break
			}
		}
	}

	// Fall back to root if we couldn't find other users
	if testUser == nil {
		testUser, _ = user.Lookup("root")
		t.Logf("Using fallback test user: root")
	}
	if testUser2 == nil {
		testUser2 = testUser // Use same user twice if we only found one
		t.Logf("Using same user for second test user")
	}

	// Try to find a suitable test group
	if gid, err := user.LookupGroupId(testUser.Gid); err == nil {
		testGroup = gid
		t.Logf("Using test group: %s (GID %s)", testGroup.Name, testGroup.Gid)
	}

	ts := &TestSSSD{
		TempDir:    tempDir,
		SocketsDir: socketsDir,
		SocketPath: filepath.Join(sssdInstallDir, "pipes", "nss"),
		t:          t,
		TestUser:   testUser,
		TestUser2:  testUser2,
		TestGroup:  testGroup,
	}

	// Start our custom-built SSSD
	sssdBin := filepath.Join(sssdInstallDir, "sbin", "sssd")
	ts.Cmd = exec.Command(sssdBin,
		"--interactive",
		"--config", confPath,
		"--logger=stderr",
		"--debug-level=0x3ff0", // Maximum debug output
	)

	// Set environment variables
	ts.Cmd.Env = append(os.Environ(),
		fmt.Sprintf("LD_LIBRARY_PATH=%s/lib:%s/lib/sssd", sssdInstallDir, sssdInstallDir),
		fmt.Sprintf("LDB_MODULES_PATH=%s/lib/ldb", sssdInstallDir),
		"SSS_NSS_USE_MEMCACHE=NO",
	)

	// Capture stderr for debugging (only in verbose mode)
	if testing.Verbose() {
		ts.Cmd.Stderr = os.Stderr
	}

	// Change working directory to temp dir
	ts.Cmd.Dir = tempDir

	if err := ts.Cmd.Start(); err != nil {
		_ = os.RemoveAll(tempDir)
		t.Fatalf("Failed to start sssd: %v", err)
	}

	// Wait for socket to be available with longer timeout
	t.Logf("Waiting for SSSD socket at %s", ts.SocketPath)
	deadline := time.Now().Add(30 * time.Second)
	socketReady := false
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ts.SocketPath); err == nil {
			// Socket exists, verify we can access it
			socketReady = true
			t.Logf("SSSD socket found, waiting for service to be fully ready and enumerate users...")
			// Give SSSD time to enumerate users from the proxy provider
			time.Sleep(2 * time.Second)
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	if !socketReady {
		// Timeout waiting for socket
		ts.Cleanup()
		t.Fatal("Timeout waiting for SSSD socket to be created")
		return nil
	}

	t.Logf("Test SSSD started successfully at %s", ts.SocketPath)
	return ts
}

// Cleanup stops the SSSD instance and removes temporary files
func (ts *TestSSSD) Cleanup() {
	if ts.Cmd != nil && ts.Cmd.Process != nil {
		// Try graceful shutdown first
		_ = ts.Cmd.Process.Signal(os.Interrupt)

		// Wait up to 2 seconds for graceful shutdown
		done := make(chan error, 1)
		go func() {
			done <- ts.Cmd.Wait()
		}()

		select {
		case <-done:
			// Process exited
		case <-time.After(2 * time.Second):
			// Force kill after timeout
			_ = ts.Cmd.Process.Kill()
			_ = ts.Cmd.Wait()
		}
	}

	if ts.TempDir != "" {
		_ = os.RemoveAll(ts.TempDir)
	}
	if ts.SocketsDir != "" {
		_ = os.RemoveAll(ts.SocketsDir)
	}
}
