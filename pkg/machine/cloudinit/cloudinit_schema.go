//go:build amd64 || arm64

package cloudinit

// CloudConfig represents a cloud-init cloud-config YAML document.
// See: https://cloudinit.readthedocs.io/en/latest/reference/modules.html
type CloudConfig struct {
	BootCmd    []string    `yaml:"bootcmd,omitempty"`
	Users      []User      `yaml:"users,omitempty"`
	WriteFiles []WriteFile `yaml:"write_files,omitempty"`
	RunCmd     []string    `yaml:"runcmd,omitempty"`
	Timezone   string      `yaml:"timezone,omitempty"`
	Hostname   string      `yaml:"hostname,omitempty"`
}

// User represents a cloud-init user configuration.
type User struct {
	Name              string   `yaml:"name"`
	UID               *int     `yaml:"uid,omitempty"`
	Groups            string   `yaml:"groups,omitempty"`
	Sudo              string   `yaml:"sudo,omitempty"`
	Shell             string   `yaml:"shell,omitempty"`
	SSHAuthorizedKeys []string `yaml:"ssh_authorized_keys,omitempty"`
	// ShouldExist when false indicates the user should be removed.
	// Cloud-init doesn't have a direct equivalent; we handle this via runcmd.
	ShouldExist *bool `yaml:"-"`
}

// WriteFile represents a file to write via cloud-init.
type WriteFile struct {
	Path        string `yaml:"path"`
	Content     string `yaml:"content,omitempty"`
	Owner       string `yaml:"owner,omitempty"`
	Permissions string `yaml:"permissions,omitempty"`
	Encoding    string `yaml:"encoding,omitempty"`
}

// MetaData represents the NoCloud meta-data file.
type MetaData struct {
	InstanceID    string `yaml:"instance-id"`
	LocalHostname string `yaml:"local-hostname"`
}

// SystemdUnit holds information about a systemd unit to be configured
// via cloud-init write_files + runcmd.
type SystemdUnit struct {
	Name     string
	Contents *string
	Enabled  *bool
	Mask     *bool
}
