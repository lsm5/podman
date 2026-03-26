//go:build amd64 || arm64

package cloudinit

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"

	"github.com/containers/podman/v6/pkg/machine/define"
	"github.com/containers/podman/v6/pkg/machine/ignition"
	"github.com/sirupsen/logrus"
	"go.podman.io/storage/pkg/fileutils"
	"gopkg.in/yaml.v3"
)

const (
	PodmanDockerTmpConfPath  = "/etc/tmpfiles.d/podman-docker.conf"
	DefaultCloudInitUserName = "core"
)

// DynamicCloudInit holds the configuration for generating a cloud-init cloud-config.
type DynamicCloudInit struct {
	Name      string
	Key       string
	TimeZone  string
	UID       int
	VMName    string
	VMType    define.VMType
	WritePath string // directory where user-data and meta-data will be written
	Cfg       CloudConfig
	Rootful   bool
	Swap      uint64
}

func (ci *DynamicCloudInit) Write() error {
	// Write user-data
	userData, err := ci.marshalUserData()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(ci.WritePath, "user-data"), userData, 0o644); err != nil {
		return err
	}

	// Write meta-data
	metaData := MetaData{
		InstanceID:    ci.VMName,
		LocalHostname: ci.VMName,
	}
	metaBytes, err := yaml.Marshal(metaData)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(ci.WritePath, "meta-data"), metaBytes, 0o644)
}

func (ci *DynamicCloudInit) marshalUserData() ([]byte, error) {
	b, err := yaml.Marshal(ci.Cfg)
	if err != nil {
		return nil, err
	}
	// cloud-config files must start with #cloud-config
	return append([]byte("#cloud-config\n"), b...), nil
}

func (ci *DynamicCloudInit) getUsers() []User {
	var users []User

	isCoreUser := ci.Name == DefaultCloudInitUserName

	// If not using the 'core' user, mark it for removal via runcmd
	if !isCoreUser {
		shouldExist := false
		users = append(users, User{
			Name:        DefaultCloudInitUserName,
			ShouldExist: &shouldExist,
		})
	}

	user := User{
		Name:              ci.Name,
		UID:               &ci.UID,
		Shell:             "/bin/bash",
		Sudo:              "ALL=(ALL) NOPASSWD:ALL",
		SSHAuthorizedKeys: []string{ci.Key},
	}

	if !isCoreUser {
		user.Groups = "sudo,adm,wheel,systemd-journal"
	}

	root := User{
		Name:              "root",
		SSHAuthorizedKeys: []string{ci.Key},
	}

	users = append(users, user, root)
	return users
}

// GenerateCloudInitConfig generates the cloud-config equivalent of the Ignition config.
func (ci *DynamicCloudInit) GenerateCloudInitConfig() error {
	if len(ci.Name) < 1 {
		ci.Name = DefaultCloudInitUserName
	}

	ci.Cfg.Users = ci.getUsers()
	ci.Cfg.WriteFiles = getWriteFiles(ci.Name, ci.UID, ci.Rootful, ci.VMType, ci.Swap)
	ci.Cfg.RunCmd = getRunCmds(ci.Name, ci.VMType, ci.Swap)

	// Handle timezone
	if len(ci.TimeZone) > 0 {
		tz := ci.TimeZone
		if tz == "local" {
			if env, ok := os.LookupEnv("TZ"); ok {
				tz = env
			} else {
				var err error
				tz, err = getLocalTimeZone()
				if err != nil {
					return fmt.Errorf("error getting local timezone: %q", err)
				}
			}
		}
		if tz == "" {
			logrus.Info("Unable to determine local timezone, machine will default to UTC")
		} else {
			ci.Cfg.Timezone = tz
		}
	}

	// Handle users that should not exist — must run in bootcmd (before
	// cloud-init's users module) so the UID is freed for the new user.
	for _, u := range ci.Cfg.Users {
		if u.ShouldExist != nil && !*u.ShouldExist {
			ci.Cfg.BootCmd = append(ci.Cfg.BootCmd,
				fmt.Sprintf("userdel -r %s || true", u.Name))
		}
	}

	// Filter out users with ShouldExist=false from the users list
	// (cloud-init doesn't support this natively)
	filteredUsers := make([]User, 0, len(ci.Cfg.Users))
	for _, u := range ci.Cfg.Users {
		if u.ShouldExist == nil || *u.ShouldExist {
			filteredUsers = append(filteredUsers, u)
		}
	}
	ci.Cfg.Users = filteredUsers

	// Rosetta for Apple Silicon
	if ci.VMType == define.AppleHvVirt && runtime.GOARCH == "arm64" {
		ci.Cfg.RunCmd = append(ci.Cfg.RunCmd, "systemctl enable --now rosetta-activation.service")
	}

	return nil
}

func getWriteFiles(usrName string, uid int, rootful bool, vmtype define.VMType, swap uint64) []WriteFile {
	var files []WriteFile

	// Containers config — written as root because the user may not exist
	// yet when write_files runs. Ownership is fixed by chown in runcmd.
	containers := "[containers]\nnetns=\"bridge\"\npids_limit=0\n"
	files = append(files, WriteFile{
		Path:        "/home/" + usrName + "/.config/containers/containers.conf",
		Content:     containers,
		Owner:       "root:root",
		Permissions: "0744",
	})

	// subuid/subgid
	subUID := 100000
	subUIDs := 1000000
	if uid >= subUID && uid < (subUID+subUIDs) {
		subUID = uid + 1
	}
	etcSubUID := fmt.Sprintf("%s:%d:%d\n", usrName, subUID, subUIDs)

	for _, sub := range []string{"/etc/subuid", "/etc/subgid"} {
		files = append(files, WriteFile{
			Path:        sub,
			Content:     etcSubUID,
			Owner:       "root:root",
			Permissions: "0744",
		})
	}

	// Linger file
	files = append(files, WriteFile{
		Path:        "/var/lib/systemd/linger/" + usrName,
		Content:     "",
		Owner:       "root:root",
		Permissions: "0644",
	})

	// Machine marker file
	files = append(files, WriteFile{
		Path:        "/etc/containers/podman-machine",
		Content:     fmt.Sprintf("%s\n", vmtype.String()),
		Owner:       "root:root",
		Permissions: "0644",
	})

	// Docker socket tmpfiles config
	files = append(files, WriteFile{
		Path:        PodmanDockerTmpConfPath,
		Content:     ignition.GetPodmanDockerTmpConfig(uid, rootful, true),
		Permissions: "0644",
	})

	// Swap via zram
	if swap > 0 {
		files = append(files, WriteFile{
			Path:        "/etc/systemd/zram-generator.conf",
			Content:     fmt.Sprintf("[zram0]\nzram-size=%d\n", swap),
			Permissions: "0644",
		})
	}

	// Copy host certs
	userHome, err := os.UserHomeDir()
	if err != nil {
		logrus.Warnf("Unable to copy certs via cloud-init: %s", err.Error())
		return files
	}

	certFiles := getCertWriteFiles(filepath.Join(userHome, ".config/containers/certs.d"), true)
	files = append(files, certFiles...)

	certFiles = getCertWriteFiles(filepath.Join(userHome, ".config/docker/certs.d"), true)
	files = append(files, certFiles...)

	sslCertFileName, ok := os.LookupEnv(sslCertFile)
	if ok {
		if err := fileutils.Exists(sslCertFileName); err == nil {
			certFiles = getCertWriteFiles(sslCertFileName, false)
			files = append(files, certFiles...)
		} else {
			logrus.Warnf("Invalid path in %s: %q", sslCertFile, err)
		}
	}

	sslCertDirName, ok := os.LookupEnv(sslCertDir)
	if ok {
		if err := fileutils.Exists(sslCertDirName); err == nil {
			certFiles = getCertWriteFiles(sslCertDirName, true)
			files = append(files, certFiles...)
		} else {
			logrus.Warnf("Invalid path in %s: %q", sslCertDir, err)
		}
	}
	if sslCertFileName != "" || sslCertDirName != "" {
		files = append(files, getSSLEnvironmentWriteFiles(sslCertFileName, sslCertDirName)...)
	}

	return files
}

func getRunCmds(usrName string, _ define.VMType, _ uint64) []string {
	cmds := []string{
		// Create directories that cloud-init's write_files may need
		fmt.Sprintf("mkdir -p /home/%s/.config/containers", usrName),
		fmt.Sprintf("mkdir -p /home/%s/.config/systemd/user", usrName),
		fmt.Sprintf("chown -R %s:%s /home/%s/.config", usrName, usrName, usrName),

		// Enable podman socket
		"systemctl enable podman.socket",

		// Disable zincati (FCOS auto-updater)
		"systemctl disable zincati.service || true",

		// Create symlinks
		"ln -sf /usr/lib/systemd/user/podman.socket /etc/systemd/user/sockets.target.wants/podman.socket",
		"ln -sf /usr/bin/podman /usr/local/bin/docker",

		// Apply tmpfiles.d config for docker.sock symlink (cloud-init
		// writes the config after systemd-tmpfiles has already run at boot)
		fmt.Sprintf("systemd-tmpfiles --create %s", PodmanDockerTmpConfPath),
	}

	return cmds
}

const (
	systemdSSLConf = "/etc/systemd/system.conf.d/podman-machine-ssl.conf"
	envdSSLConf    = "/etc/environment.d/podman-machine-ssl.conf"
	profileSSLConf = "/etc/profile.d/podman-machine-ssl.sh"
	sslCertFile    = "SSL_CERT_FILE"
	sslCertDir     = "SSL_CERT_DIR"
)

func getCertWriteFiles(certsDir string, isDir bool) []WriteFile {
	var files []WriteFile

	if isDir {
		err := filepath.WalkDir(certsDir, func(fpath string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				certPath, err := filepath.Rel(certsDir, fpath)
				if err != nil {
					logrus.Warnf("%s", err)
					return nil
				}
				file, err := prepareCertWriteFile(filepath.Join(certsDir, certPath), certPath)
				if err == nil {
					files = append(files, file)
				}
			}
			return nil
		})
		if err != nil {
			if !os.IsNotExist(err) {
				logrus.Warnf("Unable to copy certs via cloud-init, error while reading certs from %s: %s", certsDir, err.Error())
			}
		}
	} else {
		fileName := filepath.Base(certsDir)
		file, err := prepareCertWriteFile(certsDir, fileName)
		if err == nil {
			files = append(files, file)
		}
	}

	return files
}

func prepareCertWriteFile(fpath string, name string) (WriteFile, error) {
	b, err := os.ReadFile(fpath)
	if err != nil {
		logrus.Warnf("Unable to read cert file %v", err)
		return WriteFile{}, err
	}

	targetPath := path.Join(define.UserCertsTargetPath, name)
	logrus.Debugf("Copying cert file from '%s' to '%s'.", fpath, targetPath)

	return WriteFile{
		Path:        targetPath,
		Content:     string(b),
		Owner:       "root:root",
		Permissions: "0644",
	}, nil
}

func getSSLEnvironmentWriteFiles(sslFileName, sslDirName string) []WriteFile {
	systemdFileContent := "[Manager]\n"
	envdFileContent := ""
	profileFileContent := ""
	if sslFileName != "" {
		env := fmt.Sprintf("%s=%q\n", sslCertFile, path.Join(define.UserCertsTargetPath, filepath.Base(sslFileName)))
		systemdFileContent += "DefaultEnvironment=" + env
		envdFileContent += env
		profileFileContent += "export " + env
	}
	if sslDirName != "" {
		env := fmt.Sprintf("%s=%q\n", sslCertDir, define.UserCertsTargetPath)
		systemdFileContent += "DefaultEnvironment=" + env
		envdFileContent += env
		profileFileContent += "export " + env
	}
	return []WriteFile{
		{Path: systemdSSLConf, Content: systemdFileContent, Owner: "root:root", Permissions: "0644"},
		{Path: envdSSLConf, Content: envdFileContent, Owner: "root:root", Permissions: "0644"},
		{Path: profileSSLConf, Content: profileFileContent, Owner: "root:root", Permissions: "0644"},
	}
}

// CloudInitBuilder builds a cloud-init configuration, analogous to IgnitionBuilder.
type CloudInitBuilder struct {
	dynamicCloudInit DynamicCloudInit
	units            []SystemdUnit
}

// NewCloudInitBuilder creates a new CloudInitBuilder.
func NewCloudInitBuilder(dynamicCloudInit DynamicCloudInit) CloudInitBuilder {
	return CloudInitBuilder{
		dynamicCloudInit: dynamicCloudInit,
		units:            []SystemdUnit{},
	}
}

// GenerateCloudInitConfig generates the cloud-config.
func (b *CloudInitBuilder) GenerateCloudInitConfig() error {
	return b.dynamicCloudInit.GenerateCloudInitConfig()
}

// WithUnit adds systemd units. Units with Contents are written as files;
// units are enabled/disabled via runcmd.
func (b *CloudInitBuilder) WithUnit(units ...SystemdUnit) {
	b.units = append(b.units, units...)
}

// WithFile adds files to write via cloud-init.
func (b *CloudInitBuilder) WithFile(files ...WriteFile) {
	b.dynamicCloudInit.Cfg.WriteFiles = append(b.dynamicCloudInit.Cfg.WriteFiles, files...)
}

// BuildWithCloudInitDir copies user-provided cloud-init files into the write path.
func (b *CloudInitBuilder) BuildWithCloudInitDir(srcDir string) error {
	// Copy user-data and meta-data from srcDir to WritePath
	for _, name := range []string{"user-data", "meta-data"} {
		src := filepath.Join(srcDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			if os.IsNotExist(err) && name == "meta-data" {
				continue // meta-data is optional
			}
			return err
		}
		if err := os.WriteFile(filepath.Join(b.dynamicCloudInit.WritePath, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Build finalizes the cloud-config by converting accumulated units into
// write_files + runcmd entries, then writes user-data and meta-data.
func (b *CloudInitBuilder) Build() error {
	// Convert systemd units into write_files entries
	for _, unit := range b.units {
		if unit.Contents != nil {
			b.dynamicCloudInit.Cfg.WriteFiles = append(b.dynamicCloudInit.Cfg.WriteFiles, WriteFile{
				Path:        fmt.Sprintf("/etc/systemd/system/%s", unit.Name),
				Content:     *unit.Contents,
				Permissions: "0644",
			})
		}
	}

	if len(b.units) > 0 {
		// Reload systemd first so it picks up newly written unit files
		b.dynamicCloudInit.Cfg.RunCmd = append(b.dynamicCloudInit.Cfg.RunCmd, "systemctl daemon-reload")

		// Then enable/disable/mask and start units
		for _, unit := range b.units {
			if unit.Mask != nil && *unit.Mask {
				b.dynamicCloudInit.Cfg.RunCmd = append(b.dynamicCloudInit.Cfg.RunCmd,
					fmt.Sprintf("systemctl mask %s", unit.Name))
			}
			if unit.Enabled != nil {
				if *unit.Enabled {
					b.dynamicCloudInit.Cfg.RunCmd = append(b.dynamicCloudInit.Cfg.RunCmd,
						fmt.Sprintf("systemctl enable --now %s", unit.Name))
				} else {
					b.dynamicCloudInit.Cfg.RunCmd = append(b.dynamicCloudInit.Cfg.RunCmd,
						fmt.Sprintf("systemctl disable %s", unit.Name))
				}
			}
		}
	}

	logrus.Debugf("writing cloud-init files to %q", b.dynamicCloudInit.WritePath)
	return b.dynamicCloudInit.Write()
}

// AddPlaybook adds an ansible playbook to be run on first boot.
func (b *CloudInitBuilder) AddPlaybook(contents string, destPath string, username string) error {
	b.WithFile(WriteFile{
		Path:        destPath,
		Content:     contents,
		Owner:       fmt.Sprintf("%s:%s", username, username),
		Permissions: "0744",
	})

	// Create a first-boot playbook service via runcmd
	unitContent := fmt.Sprintf(`[Unit]
After=ready.service
ConditionFirstBoot=yes

[Service]
Type=oneshot
User=%s
Group=%s
ExecStart=ansible-playbook %s

[Install]
WantedBy=default.target
`, username, username, destPath)

	b.WithUnit(SystemdUnit{
		Name:     "playbook.service",
		Contents: &unitContent,
		Enabled:  boolToPtr(true),
	})

	return nil
}

// GetWritePath returns the path where cloud-init files are stored.
func (b *CloudInitBuilder) GetWritePath() string {
	return b.dynamicCloudInit.WritePath
}

func boolToPtr(b bool) *bool {
	return &b
}

