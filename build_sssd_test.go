//go:build linux

package gosssd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
)

var (
	sssdBuildDir     string
	sssdBuildOnce    sync.Once
	sssdBuildErr     error
	sssdBuildSuccess bool
	sssdBuildLock    *os.File // Keep lock file open for duration of test run

	// Expected SHA256 checksum for SSSD 2.9.7 tarball
	sssd297SHA256 = "6b5284a4d72b67c0897699794360d79e0f67461957e20273c2649f025e76c248"

	// Build directory relative to source root
	sssdBuildDirName = ".sssd-build"
)

// BuildCustomSSSD builds SSSD 2.9.7 with custom paths for testing.
// The build is cached and only done once per test run.
// This function acquires and holds a lock for the entire test session since
// only one test can use the SSSD pipes at a time.
// If the GOSSSD_FAIL_BUILD_REQUIRED environment variable is set to "true",
// build failures will cause test failures instead of being skipped.
func BuildCustomSSSD(t *testing.T) (string, error) {
	t.Helper()

	sssdBuildOnce.Do(func() {
		// Determine build root in source tree - must be absolute path
		buildRoot, err := filepath.Abs(sssdBuildDirName)
		if err != nil {
			sssdBuildErr = fmt.Errorf("failed to get absolute path: %w", err)
			return
		}
		installDir := filepath.Join(buildRoot, "install")
		stampFile := filepath.Join(buildRoot, "build.stamp")

		// Acquire exclusive lock on stamp file to prevent parallel builds
		// AND to ensure only one test uses SSSD at a time (single global pipe)
		if err := os.MkdirAll(buildRoot, 0755); err != nil {
			sssdBuildErr = fmt.Errorf("failed to create build dir: %w", err)
			return
		}

		sssdBuildLock, err = os.OpenFile(stampFile, os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			sssdBuildErr = fmt.Errorf("failed to open lock file: %w", err)
			return
		}
		// NOTE: We do NOT defer Close() here - lock must be held for entire test run!
		// The lock will be released when the test process exits

		// Acquire exclusive lock (blocks if another test is building or running)
		t.Logf("Acquiring SSSD build/test lock...")
		if err := syscall.Flock(int(sssdBuildLock.Fd()), syscall.LOCK_EX); err != nil {
			sssdBuildErr = fmt.Errorf("failed to acquire lock: %w", err)
			_ = sssdBuildLock.Close()
			sssdBuildLock = nil
			return
		}
		t.Logf("SSSD lock acquired")

		// Determine current user for SSSD configuration
		currentUser := "root"
		if u, err := user.Current(); err == nil && u.Username != "" {
			currentUser = u.Username
		}

		// Check if already built (after acquiring lock)
		sssdBin := filepath.Join(installDir, "sbin", "sssd")
		needsRebuild := false
		if _, err := os.Stat(sssdBin); err == nil {
			// Verify stamp file indicates successful build and check if user changed
			content, _ := os.ReadFile(stampFile)
			stampUser := string(content)
			if stampUser == currentUser {
				t.Logf("Using cached SSSD build at %s", installDir)
				sssdBuildDir = installDir
				sssdBuildSuccess = true
				return
			}
			// User changed, need to rebuild
			t.Logf("User changed from %s to %s, rebuilding SSSD...", stampUser, currentUser)
			needsRebuild = true
		}

		// Clean up old installation if rebuilding
		if needsRebuild {
			t.Logf("Cleaning up old installation...")
			if err := os.RemoveAll(installDir); err != nil {
				sssdBuildErr = fmt.Errorf("failed to remove old installation: %w", err)
				return
			}
		}

		t.Logf("Building custom SSSD 2.9.7 (this may take several minutes)...")

		srcDir := filepath.Join(buildRoot, "src")
		// Create directories
		if err := os.MkdirAll(srcDir, 0755); err != nil {
			sssdBuildErr = fmt.Errorf("failed to create src dir: %w", err)
			return
		}

		// Download SSSD source (if not already present with correct checksum)
		tarball := "sssd-2.9.7.tar.gz"
		tarballPath := filepath.Join(buildRoot, tarball)

		// Verify existing tarball or download
		needsDownload := false
		if _, err := os.Stat(tarballPath); err == nil {
			// Tarball exists, verify checksum
			t.Logf("Verifying existing tarball checksum...")
			file, err := os.Open(tarballPath)
			if err != nil {
				sssdBuildErr = fmt.Errorf("failed to open tarball for verification: %w", err)
				return
			}
			hasher := sha256.New()
			if _, err := io.Copy(hasher, file); err != nil {
				_ = file.Close()
				sssdBuildErr = fmt.Errorf("failed to compute SHA256: %w", err)
				return
			}
			_ = file.Close()

			actualChecksum := hex.EncodeToString(hasher.Sum(nil))
			if actualChecksum != sssd297SHA256 {
				t.Logf("Checksum mismatch (expected %s, got %s), removing and re-downloading...",
					sssd297SHA256, actualChecksum)
				if err := os.Remove(tarballPath); err != nil {
					sssdBuildErr = fmt.Errorf("failed to remove invalid tarball: %w", err)
					return
				}
				needsDownload = true
			} else {
				t.Logf("Existing tarball checksum verified successfully")
			}
		} else {
			needsDownload = true
		}

		if needsDownload {
			t.Logf("Downloading SSSD 2.9.7...")
			downloadCmd := exec.Command("curl", "-L", "-o", tarballPath,
				"https://github.com/SSSD/sssd/releases/download/2.9.7/sssd-2.9.7.tar.gz")
			downloadCmd.Stdout = os.Stdout
			downloadCmd.Stderr = os.Stderr
			if err := downloadCmd.Run(); err != nil {
				sssdBuildErr = fmt.Errorf("failed to download SSSD: %w", err)
				return
			}

			// Verify downloaded tarball checksum
			t.Logf("Verifying downloaded tarball checksum...")
			file, err := os.Open(tarballPath)
			if err != nil {
				sssdBuildErr = fmt.Errorf("failed to open tarball for verification: %w", err)
				return
			}
			hasher := sha256.New()
			if _, err := io.Copy(hasher, file); err != nil {
				_ = file.Close()
				sssdBuildErr = fmt.Errorf("failed to compute SHA256: %w", err)
				return
			}
			_ = file.Close()

			actualChecksum := hex.EncodeToString(hasher.Sum(nil))
			if actualChecksum != sssd297SHA256 {
				sssdBuildErr = fmt.Errorf("SHA256 checksum mismatch: expected %s, got %s", sssd297SHA256, actualChecksum)
				return
			}
			t.Logf("Downloaded tarball checksum verified successfully")
		}

		// Extract
		t.Logf("Extracting SSSD source...")
		extractCmd := exec.Command("tar", "-xzf", tarballPath, "-C", buildRoot)
		if err := extractCmd.Run(); err != nil {
			sssdBuildErr = fmt.Errorf("failed to extract SSSD: %w", err)
			return
		}

		sssdSrcDir := filepath.Join(buildRoot, "sssd-2.9.7")

		// Apply patches to remove UID/GID checks for non-root execution
		t.Logf("Applying patches to SSSD source...")
		patches := []struct {
			file    string
			old     string
			new     string
			comment string
		}{
			{
				file:    "src/monitor/monitor.c",
				old:     "    poptFreeContext(pc);\n\n    uid = getuid();\n    if (uid != 0) {\n        ERROR(\"Running under %\"PRIu64\", must be root\\n\", (uint64_t) uid);\n        sss_log(SSS_LOG_ALERT, \"sssd must be run as root\");\n        return 8;\n    }\n\n    tmp_ctx = talloc_new(NULL);",
				new:     "    poptFreeContext(pc);\n\n    tmp_ctx = talloc_new(NULL);",
				comment: "Remove root user check in monitor",
			},
			{
				file:    "src/util/sss_ini.c",
				old:     "        return EOK;\n    }\n\n    return ini_config_access_check(self->file,\n                                   INI_ACCESS_CHECK_MODE |\n                                   INI_ACCESS_CHECK_UID |\n                                   INI_ACCESS_CHECK_GID,\n                                   0, /* owned by root */\n                                   0, /* owned by root */\n                                   S_IRUSR, /* r**------ */\n                                   ALLPERMS & ~(S_IWUSR|S_IXUSR));\n}",
				new:     "        return EOK;\n    }\n\n    return EOK;\n}",
				comment: "Skip config file ownership checks",
			},
			{
				file:    "src/util/server.c",
				old:     "                  \"Cannot chown the debug files, debugging might not work!\\n\");\n        }\n\n        ret = become_user(uid, gid);\n        if (ret != EOK) {\n            DEBUG(SSSDBG_FUNC_DATA,\n                  \"Cannot become user [%\"SPRIuid\"][%\"SPRIgid\"].\\n\", uid, gid);\n            return ret;\n        }\n    }",
				new:     "                  \"Cannot chown the debug files, debugging might not work!\\n\");\n        }\n\n        ret = EOK; // skip become_user\n    }",
				comment: "Skip become_user call",
			},
			{
				file:    "src/sbus/server/sbus_server.c",
				old:     "        if (ret != EOK) {\n            ret = errno;\n            DEBUG(SSSDBG_CRIT_FAILURE, \"chmod failed for [%s] [%d]: %s\\n\",\n                  filename, ret, sss_strerror(ret));\n            return ret;\n        }\n    }\n\n    if (stat_buf.st_uid != uid || stat_buf.st_gid != gid) {\n        ret = chown(filename, uid, gid);\n        if (ret != EOK) {\n            ret = errno;\n            DEBUG(SSSDBG_CRIT_FAILURE, \"chown failed for [%s] [%d]: %s\\n\",\n                  filename, ret, sss_strerror(ret));\n            return ret;\n        }\n    }",
				new:     "        if (ret != EOK) {\n            ret = errno;\n            DEBUG(SSSDBG_CRIT_FAILURE, \"chmod failed for [%s] [%d]: %s\\n\",\n                  filename, ret, sss_strerror(ret));\n            return ret;\n        }\n    }",
				comment: "Skip socket file ownership check",
			},
		}

		for _, patch := range patches {
			filePath := filepath.Join(sssdSrcDir, patch.file)
			t.Logf("Patching %s: %s", patch.file, patch.comment)

			content, err := os.ReadFile(filePath)
			if err != nil {
				sssdBuildErr = fmt.Errorf("failed to read %s: %w", patch.file, err)
				return
			}

			// Apply patch by replacing old text with new text
			oldContent := string(content)
			newContent := strings.Replace(oldContent, patch.old, patch.new, 1)

			if oldContent == newContent {
				sssdBuildErr = fmt.Errorf("patch failed for %s: old text not found", patch.file)
				return
			}

			if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
				sssdBuildErr = fmt.Errorf("failed to write patched %s: %w", patch.file, err)
				return
			}
		}

		// Configure with custom paths
		t.Logf("Configuring SSSD build...")

		configureArgs := []string{
			fmt.Sprintf("--prefix=%s", installDir),
			fmt.Sprintf("--libdir=%s/lib", installDir),
			fmt.Sprintf("--with-db-path=%s/db", installDir),
			fmt.Sprintf("--with-pid-path=%s/run", installDir),
			fmt.Sprintf("--with-log-path=%s/log", installDir),
			fmt.Sprintf("--with-pipe-path=%s/pipes", installDir),
			fmt.Sprintf("--with-pubconf-path=%s/etc", installDir),
			fmt.Sprintf("--with-mcache-path=%s/mcache", installDir),
			fmt.Sprintf("--with-ldb-lib-dir=%s/lib/ldb", installDir),
			fmt.Sprintf("--with-app-libs=%s/lib", installDir),
		}

		// Only set --with-sssd-user for non-root users
		if currentUser != "root" {
			configureArgs = append(configureArgs, fmt.Sprintf("--with-sssd-user=%s", currentUser))
		}

		configureArgs = append(configureArgs,
			"--disable-cifs-idmap-plugin",
			"--without-python2-bindings",
			// sssd has a bug where, if python3 is not built, then
			// it tries to install the analyzer to /modules/sssd
			// instead
			"--with-python3-bindings",
			"--without-selinux",
			"--without-semanage",
			"--disable-krb5-locator-plugin",
			"--disable-pac-responder",
			"--without-kcm",
			"--without-samba",
			"--without-oidc-child",
			"--without-manpages",
			"--without-sudo",
			"--without-autofs",
			"--without-ssh",
			"--without-nfsv4-idmapd-plugin",
			"--disable-polkit-rules-path",
		)

		configureCmd := exec.Command("./configure", configureArgs...)
		configureCmd.Dir = sssdSrcDir
		configureCmd.Stdout = os.Stdout
		configureCmd.Stderr = os.Stderr
		configureCmd.Env = append(os.Environ(),
			fmt.Sprintf("PKG_CONFIG_PATH=%s/lib/pkgconfig", installDir),
		)

		if err := configureCmd.Run(); err != nil {
			sssdBuildErr = fmt.Errorf("failed to configure SSSD: %w", err)
			return
		}

		// Build with parallel jobs
		t.Logf("Building SSSD (using %d cores)...", runtime.NumCPU())
		makeCmd := exec.Command("make", fmt.Sprintf("-j%d", runtime.NumCPU()))
		makeCmd.Dir = sssdSrcDir
		makeCmd.Stdout = os.Stdout
		makeCmd.Stderr = os.Stderr
		if err := makeCmd.Run(); err != nil {
			sssdBuildErr = fmt.Errorf("failed to build SSSD: %w", err)
			return
		}

		// Install
		t.Logf("Installing SSSD...")
		installCmd := exec.Command("make", "install")
		installCmd.Dir = sssdSrcDir
		installCmd.Stdout = os.Stdout
		installCmd.Stderr = os.Stderr
		if err := installCmd.Run(); err != nil {
			sssdBuildErr = fmt.Errorf("failed to install SSSD: %w", err)
			return
		}

		// Create necessary runtime directories
		runtimeDirs := []string{
			filepath.Join(installDir, "db"),
			filepath.Join(installDir, "run"),
			filepath.Join(installDir, "log"),
			filepath.Join(installDir, "pipes"),
			filepath.Join(installDir, "pipes", "private"),
			filepath.Join(installDir, "mcache"),
			filepath.Join(installDir, "etc"),
		}
		for _, dir := range runtimeDirs {
			if err := os.MkdirAll(dir, 0755); err != nil {
				sssdBuildErr = fmt.Errorf("failed to create runtime dir %s: %w", dir, err)
				return
			}
		}

		// Write stamp file with username to indicate successful build
		if err := os.WriteFile(stampFile, []byte(currentUser), 0644); err != nil {
			sssdBuildErr = fmt.Errorf("failed to write stamp file: %w", err)
			return
		}

		sssdBuildDir = installDir
		sssdBuildSuccess = true
		t.Logf("SSSD build complete at %s", installDir)
	})

	// If build failed, return error (test should skip)
	if sssdBuildErr != nil {
		return "", sssdBuildErr
	}

	return sssdBuildDir, nil
}
