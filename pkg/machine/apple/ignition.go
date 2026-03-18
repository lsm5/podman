//go:build darwin

package apple

import (
	"net"
	"net/http"

	"github.com/containers/podman/v6/pkg/machine/define"
	"github.com/containers/podman/v6/pkg/machine/vmconfigs"
	"github.com/sirupsen/logrus"
)

// ServeIgnitionOverSock allows podman to open a small httpd instance on the vsock between the host
// and guest to serve the cloud-init CIDATA ISO (or ignition file for backwards compatibility).
func ServeIgnitionOverSock(ignitionSocket *define.VMFile, mc *vmconfigs.MachineConfig) error {
	// Try cloud-init ISO first, fall back to ignition file
	cloudInitISO, err := mc.CloudInitISO()
	if err != nil {
		return err
	}

	var fileData []byte
	fileData, err = cloudInitISO.Read()
	if err != nil {
		// Fall back to ignition file if CIDATA ISO doesn't exist
		ignitionFile, ignErr := mc.IgnitionFile()
		if ignErr != nil {
			return ignErr
		}
		logrus.Debugf("reading ignition file: %s", ignitionFile.GetPath())
		fileData, err = ignitionFile.Read()
		if err != nil {
			return err
		}
	} else {
		logrus.Debugf("reading cloud-init ISO: %s", cloudInitISO.GetPath())
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write(fileData)
		if err != nil {
			logrus.Errorf("failed to serve provisioning data: %v", err)
		}
	})
	listener, err := net.Listen("unix", ignitionSocket.GetPath())
	if err != nil {
		return err
	}
	logrus.Debugf("provisioning socket device: %s", ignitionSocket.GetPath())
	defer func() {
		if err := listener.Close(); err != nil {
			logrus.Error(err)
		}
	}()
	return http.Serve(listener, mux)
}
