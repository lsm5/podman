//go:build windows

package cloudinit

func getLocalTimeZone() (string, error) {
	return "", nil
}
