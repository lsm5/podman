package cloudinit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func getLocalTimeZone() (string, error) {
	path, err := filepath.EvalSymlinks("/etc/localtime")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}

	zoneinfo := os.Getenv("TZDIR")
	if zoneinfo == "" {
		zoneinfo = "/usr/share/zoneinfo"
	}
	return strings.TrimPrefix(path, filepath.Clean(zoneinfo)+"/"), nil
}
