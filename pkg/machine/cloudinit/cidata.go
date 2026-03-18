//go:build amd64 || arm64

package cloudinit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

// CreateCIDATAISO creates a NoCloud CIDATA ISO image from user-data and meta-data
// files in the given directory. The ISO is written to isoPath.
func CreateCIDATAISO(cloudInitDir string, isoPath string) error {
	userDataPath := filepath.Join(cloudInitDir, "user-data")
	metaDataPath := filepath.Join(cloudInitDir, "meta-data")

	// Verify required files exist
	if _, err := os.Stat(userDataPath); err != nil {
		return fmt.Errorf("user-data file not found in %s: %w", cloudInitDir, err)
	}
	if _, err := os.Stat(metaDataPath); err != nil {
		return fmt.Errorf("meta-data file not found in %s: %w", cloudInitDir, err)
	}

	// Try mkisofs first (more common on RHEL/Fedora), then genisoimage
	isoBinary, err := findISOBinary()
	if err != nil {
		return err
	}

	logrus.Debugf("creating CIDATA ISO at %s using %s", isoPath, isoBinary)

	// Create the ISO with the CIDATA volume label
	cmd := exec.Command(isoBinary,
		"-output", isoPath,
		"-volid", "cidata",
		"-joliet",
		"-rock",
		userDataPath,
		metaDataPath,
	)
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create CIDATA ISO: %w", err)
	}

	return nil
}

// findISOBinary locates an ISO creation tool on the system.
func findISOBinary() (string, error) {
	for _, name := range []string{"mkisofs", "genisoimage", "xorrisofs"} {
		path, err := exec.LookPath(name)
		if err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no ISO creation tool found; install mkisofs, genisoimage, or xorriso")
}
