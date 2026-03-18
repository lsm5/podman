//go:build freebsd

package cloudinit

func getLocalTimeZone() (string, error) {
	return "", nil
}
