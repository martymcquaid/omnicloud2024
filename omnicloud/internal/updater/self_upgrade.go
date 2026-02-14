package updater

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// getVersionFromAPI fetches version metadata from the given base URL (e.g. main server or self).
func getVersionFromAPI(baseURL, version string) (*VersionInfo, error) {
	url := strings.TrimSuffix(baseURL, "/") + "/api/v1/versions"
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch versions: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Versions []VersionInfo `json:"versions"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	for _, v := range result.Versions {
		if v.Version == version {
			return &v, nil
		}
	}

	return nil, fmt.Errorf("version %s not found", version)
}

// downloadFromBase downloads a file from baseURL + downloadURL into destPath.
func downloadFromBase(baseURL, downloadURL, destPath string) error {
	url := strings.TrimSuffix(baseURL, "/") + downloadURL
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// PerformSelfUpgrade downloads the given version from baseURL, verifies it, and replaces the current binary.
// The caller is responsible for restarting the process (e.g. SIGTERM) after a successful return.
// baseURL should be the main server URL (e.g. "http://127.0.0.1:10858" when upgrading self).
func PerformSelfUpgrade(baseURL, targetVersion string) error {
	log.Printf("Self-upgrade: starting upgrade to %s from %s", targetVersion, baseURL)

	versionInfo, err := getVersionFromAPI(baseURL, targetVersion)
	if err != nil {
		return fmt.Errorf("get version info: %w", err)
	}

	packagePath := filepath.Join(os.TempDir(), fmt.Sprintf("omnicloud-%s.tar.gz", targetVersion))
	defer os.Remove(packagePath)

	if err := downloadFromBase(baseURL, versionInfo.DownloadURL, packagePath); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	if err := verifyChecksumFile(packagePath, versionInfo.Checksum); err != nil {
		os.Remove(packagePath)
		return fmt.Errorf("checksum: %w", err)
	}

	log.Println("Self-upgrade: package downloaded and verified")

	stagingDir := filepath.Join(os.TempDir(), fmt.Sprintf("omnicloud-upgrade-%s", targetVersion))
	os.RemoveAll(stagingDir)
	defer os.RemoveAll(stagingDir)

	if err := extractPackageFile(packagePath, stagingDir); err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("executable path: %w", err)
	}

	// Tarball layout: omnicloud-{version}-linux-amd64/omnicloud
	newBinary := filepath.Join(stagingDir, fmt.Sprintf("omnicloud-%s-linux-amd64", targetVersion), "omnicloud")
	if _, err := os.Stat(newBinary); err != nil {
		return fmt.Errorf("new binary not found at %s: %w", newBinary, err)
	}

	// Write to .new path: we cannot overwrite the running executable on Linux (ETXTBSY).
	// We will exec the new binary from .new; it will replace the old file and re-exec.
	newPath := execPath + ".new"
	if err := copyFile(newBinary, newPath); err != nil {
		return fmt.Errorf("write new binary: %w", err)
	}
	if err := os.Chmod(newPath, 0755); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("chmod new binary: %w", err)
	}

	log.Printf("Self-upgrade: exec-ing new binary from %s (will replace %s)", newPath, execPath)
	args := append([]string{newPath, "--post-upgrade-replace=" + execPath}, os.Args[1:]...)
	if err := syscall.Exec(newPath, args, os.Environ()); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("exec new binary: %w", err)
	}
	// Unreachable when exec succeeds
	return nil
}
