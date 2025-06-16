package utils

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

// findStorageConfPath returns the path to the current storage.conf in use.
func findStorageConfPath() (string, error) {
	if conf := os.Getenv("CONTAINERS_STORAGE_CONF"); conf != "" {
		return conf, nil
	}
	home, err := os.UserHomeDir()
	if err == nil {
		userConf := filepath.Join(home, ".config", "containers", "storage.conf")
		if _, err := os.Stat(userConf); err == nil {
			return userConf, nil
		}
	}
	if _, err := os.Stat("/etc/containers/storage.conf"); err == nil {
		return "/etc/containers/storage.conf", nil
	}
	if _, err := os.Stat("/usr/share/containers/storage.conf"); err == nil {
		return "/usr/share/containers/storage.conf", nil
	}
	return "", fmt.Errorf("could not find storage.conf to override digest_type")
}

// overrideStorageConfWithDigest creates a temp storage.conf with the requested digest_type and sets CONTAINERS_STORAGE_CONF.
// Returns the path to the temp file and a cleanup function.
func OverrideStorageConfWithDigest(digestType string) (string, func(), error) {
	origConf, err := findStorageConfPath()
	if err != nil {
		return "", nil, err
	}
	contents, err := ioutil.ReadFile(origConf)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read storage.conf: %w", err)
	}
	lines := strings.Split(string(contents), "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "digest_type") {
			lines[i] = fmt.Sprintf("digest_type = \"%s\"", digestType)
			found = true
			break
		}
	}
	if !found {
		// Add to the end
		lines = append(lines, fmt.Sprintf("digest_type = \"%s\"", digestType))
	}
	newContents := strings.Join(lines, "\n")
	tmpFile, err := ioutil.TempFile("", "storage-override-*.conf")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp storage.conf: %w", err)
	}
	if _, err := tmpFile.Write([]byte(newContents)); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("failed to write temp storage.conf: %w", err)
	}
	tmpFile.Close()
	os.Setenv("CONTAINERS_STORAGE_CONF", tmpFile.Name())
	cleanup := func() {
		os.Remove(tmpFile.Name())
	}
	return tmpFile.Name(), cleanup, nil
}

// GetDigestTypeFromStorageConf reads the digest_type from the effective storage.conf, or returns "sha256" if not set or on error.
func GetDigestTypeFromStorageConf() (string, error) {
	confPath, err := findStorageConfPath()
	if err != nil {
		return "sha256", err
	}
	contents, err := ioutil.ReadFile(confPath)
	if err != nil {
		return "sha256", err
	}
	lines := strings.Split(string(contents), "\n")
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "digest_type") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				return strings.Trim(strings.TrimSpace(parts[1]), "\"' "), nil
			}
		}
	}
	return "sha256", nil
}
