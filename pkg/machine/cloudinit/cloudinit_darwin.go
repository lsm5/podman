//go:build darwin

package cloudinit

import (
	"os"
	"strings"
)

func getLocalTimeZone() (string, error) {
	tzPath, err := os.Readlink("/etc/localtime")
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(tzPath, "/var/db/timezone/zoneinfo"), nil
}
