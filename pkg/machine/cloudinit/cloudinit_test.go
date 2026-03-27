//go:build amd64 || arm64

package cloudinit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containers/podman/v6/pkg/machine/define"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func newTestCloudInit(t *testing.T) DynamicCloudInit {
	t.Helper()
	return DynamicCloudInit{
		Name:      "core",
		Key:       "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest test@localhost",
		TimeZone:  "America/New_York",
		UID:       1000,
		VMName:    "test-machine",
		VMType:    define.QemuVirt,
		WritePath: t.TempDir(),
		Rootful:   false,
		Swap:      0,
	}
}

func TestGenerateCloudInitConfig_DefaultUser(t *testing.T) {
	ci := newTestCloudInit(t)
	require.NoError(t, ci.GenerateCloudInitConfig())

	// Default user "core" should not generate bootcmd to delete itself
	assert.Empty(t, ci.Cfg.BootCmd)

	// Should have 2 users: core + root
	require.Len(t, ci.Cfg.Users, 2)
	assert.Equal(t, "core", ci.Cfg.Users[0].Name)
	assert.Equal(t, 1000, *ci.Cfg.Users[0].UID)
	assert.Equal(t, "/bin/bash", ci.Cfg.Users[0].Shell)
	assert.Equal(t, "ALL=(ALL) NOPASSWD:ALL", ci.Cfg.Users[0].Sudo)
	assert.Contains(t, ci.Cfg.Users[0].SSHAuthorizedKeys, ci.Key)

	assert.Equal(t, "root", ci.Cfg.Users[1].Name)
	assert.Contains(t, ci.Cfg.Users[1].SSHAuthorizedKeys, ci.Key)

	// Timezone should be set
	assert.Equal(t, "America/New_York", ci.Cfg.Timezone)
}

func TestGenerateCloudInitConfig_CustomUser(t *testing.T) {
	ci := newTestCloudInit(t)
	ci.Name = "testuser"
	require.NoError(t, ci.GenerateCloudInitConfig())

	// Custom user should generate bootcmd to delete the default "core" user
	require.Len(t, ci.Cfg.BootCmd, 1)
	assert.Contains(t, ci.Cfg.BootCmd[0], "userdel -r core")

	// "core" user with ShouldExist=false should be filtered out
	for _, u := range ci.Cfg.Users {
		assert.NotEqual(t, "core", u.Name, "core user should be filtered from users list")
	}

	// Should have 2 users: testuser + root
	require.Len(t, ci.Cfg.Users, 2)
	assert.Equal(t, "testuser", ci.Cfg.Users[0].Name)
	assert.Contains(t, ci.Cfg.Users[0].Groups, "wheel")
	assert.Equal(t, "root", ci.Cfg.Users[1].Name)
}

func TestGenerateCloudInitConfig_EmptyName(t *testing.T) {
	ci := newTestCloudInit(t)
	ci.Name = ""
	require.NoError(t, ci.GenerateCloudInitConfig())

	// Empty name should default to "core"
	assert.Equal(t, "core", ci.Cfg.Users[0].Name)
}

func TestGenerateCloudInitConfig_Swap(t *testing.T) {
	ci := newTestCloudInit(t)
	ci.Swap = 2048
	require.NoError(t, ci.GenerateCloudInitConfig())

	// Should have a zram-generator.conf write_file
	found := false
	for _, f := range ci.Cfg.WriteFiles {
		if f.Path == "/etc/systemd/zram-generator.conf" {
			found = true
			assert.Contains(t, f.Content, "zram-size=2048")
			break
		}
	}
	assert.True(t, found, "zram-generator.conf should be present when swap > 0")
}

func TestGenerateCloudInitConfig_NoSwap(t *testing.T) {
	ci := newTestCloudInit(t)
	ci.Swap = 0
	require.NoError(t, ci.GenerateCloudInitConfig())

	for _, f := range ci.Cfg.WriteFiles {
		assert.NotEqual(t, "/etc/systemd/zram-generator.conf", f.Path,
			"zram-generator.conf should not be present when swap is 0")
	}
}

func TestGenerateCloudInitConfig_Rootful(t *testing.T) {
	ci := newTestCloudInit(t)
	ci.Rootful = true
	require.NoError(t, ci.GenerateCloudInitConfig())

	// Docker tmpfiles config should reference rootful paths
	found := false
	for _, f := range ci.Cfg.WriteFiles {
		if f.Path == PodmanDockerTmpConfPath {
			found = true
			// Rootful should point to /run/podman/podman.sock, not user socket
			assert.Contains(t, f.Content, "/run/podman/podman.sock")
			break
		}
	}
	assert.True(t, found, "tmpfiles.d config should be present")
}

func TestGenerateCloudInitConfig_Rootless(t *testing.T) {
	ci := newTestCloudInit(t)
	ci.Rootful = false
	require.NoError(t, ci.GenerateCloudInitConfig())

	for _, f := range ci.Cfg.WriteFiles {
		if f.Path == PodmanDockerTmpConfPath {
			// Rootless should point to user socket
			assert.Contains(t, f.Content, fmt.Sprintf("/run/user/%d/podman/podman.sock", ci.UID))
			return
		}
	}
	t.Fatal("tmpfiles.d config should be present")
}

func TestWriteFiles_Ownership(t *testing.T) {
	ci := newTestCloudInit(t)
	ci.Name = "customuser"
	require.NoError(t, ci.GenerateCloudInitConfig())

	// All write_files should use root:root as owner (not the custom user)
	// because write_files runs before the users module
	for _, f := range ci.Cfg.WriteFiles {
		if f.Owner != "" {
			assert.Equal(t, "root:root", f.Owner,
				"file %s should be owned by root:root, got %s", f.Path, f.Owner)
		}
	}
}

func TestWriteFiles_RequiredFiles(t *testing.T) {
	ci := newTestCloudInit(t)
	require.NoError(t, ci.GenerateCloudInitConfig())

	requiredPaths := []string{
		"/home/core/.config/containers/containers.conf",
		"/etc/subuid",
		"/etc/subgid",
		"/var/lib/systemd/linger/core",
		"/etc/containers/podman-machine",
		PodmanDockerTmpConfPath,
	}

	writtenPaths := make(map[string]bool)
	for _, f := range ci.Cfg.WriteFiles {
		writtenPaths[f.Path] = true
	}

	for _, p := range requiredPaths {
		assert.True(t, writtenPaths[p], "required file %s should be in write_files", p)
	}
}

func TestRunCmds_CriticalCommandsExitOnFailure(t *testing.T) {
	ci := newTestCloudInit(t)
	require.NoError(t, ci.GenerateCloudInitConfig())

	// Critical commands should have || exit 1
	criticalPatterns := []string{
		"systemctl enable podman.socket",
		"systemd-tmpfiles --create",
	}
	for _, pattern := range criticalPatterns {
		found := false
		for _, cmd := range ci.Cfg.RunCmd {
			if strings.Contains(cmd, pattern) {
				found = true
				assert.Contains(t, cmd, "|| exit 1",
					"critical command %q should exit on failure", pattern)
			}
		}
		assert.True(t, found, "command matching %q should be in runcmd", pattern)
	}
}

func TestRunCmds_OptionalCommandsDontExitOnFailure(t *testing.T) {
	ci := newTestCloudInit(t)
	require.NoError(t, ci.GenerateCloudInitConfig())

	for _, cmd := range ci.Cfg.RunCmd {
		if strings.Contains(cmd, "zincati") {
			assert.Contains(t, cmd, "|| true",
				"optional command %q should use || true", cmd)
		}
	}
}

func TestMarshalUserData_HasCloudConfigHeader(t *testing.T) {
	ci := newTestCloudInit(t)
	require.NoError(t, ci.GenerateCloudInitConfig())

	data, err := ci.marshalUserData()
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(data), "#cloud-config\n"),
		"user-data must start with #cloud-config header")
}

func TestMarshalUserData_ValidYAML(t *testing.T) {
	ci := newTestCloudInit(t)
	require.NoError(t, ci.GenerateCloudInitConfig())

	data, err := ci.marshalUserData()
	require.NoError(t, err)

	// Strip the #cloud-config header and verify it's valid YAML
	yamlContent := strings.TrimPrefix(string(data), "#cloud-config\n")
	var parsed CloudConfig
	require.NoError(t, yaml.Unmarshal([]byte(yamlContent), &parsed))
	assert.NotEmpty(t, parsed.Users)
	assert.NotEmpty(t, parsed.WriteFiles)
	assert.NotEmpty(t, parsed.RunCmd)
}

func TestWrite_CreatesFiles(t *testing.T) {
	ci := newTestCloudInit(t)
	require.NoError(t, ci.GenerateCloudInitConfig())
	require.NoError(t, ci.Write())

	// user-data should exist and start with #cloud-config
	userData, err := os.ReadFile(filepath.Join(ci.WritePath, "user-data"))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(userData), "#cloud-config\n"))

	// meta-data should exist and contain instance-id
	metaData, err := os.ReadFile(filepath.Join(ci.WritePath, "meta-data"))
	require.NoError(t, err)
	assert.Contains(t, string(metaData), "instance-id: test-machine")
	assert.Contains(t, string(metaData), "local-hostname: test-machine")
}

func TestSubUIDRange_NoOverlap(t *testing.T) {
	ci := newTestCloudInit(t)
	ci.UID = 100500 // UID within the default subuid range
	require.NoError(t, ci.GenerateCloudInitConfig())

	for _, f := range ci.Cfg.WriteFiles {
		if f.Path == "/etc/subuid" {
			// subuid start should be adjusted to avoid overlap
			assert.Contains(t, f.Content, "core:100501:1000000",
				"subuid range should start after user's UID to avoid overlap")
			return
		}
	}
	t.Fatal("/etc/subuid should be in write_files")
}

func TestSubUIDRange_Default(t *testing.T) {
	ci := newTestCloudInit(t)
	ci.UID = 1000 // default, outside the subuid range
	require.NoError(t, ci.GenerateCloudInitConfig())

	for _, f := range ci.Cfg.WriteFiles {
		if f.Path == "/etc/subuid" {
			assert.Contains(t, f.Content, "core:100000:1000000")
			return
		}
	}
	t.Fatal("/etc/subuid should be in write_files")
}

func TestMachineMarkerFile(t *testing.T) {
	for _, vmType := range []define.VMType{define.QemuVirt, define.AppleHvVirt, define.HyperVVirt} {
		t.Run(vmType.String(), func(t *testing.T) {
			ci := newTestCloudInit(t)
			ci.VMType = vmType
			require.NoError(t, ci.GenerateCloudInitConfig())

			for _, f := range ci.Cfg.WriteFiles {
				if f.Path == "/etc/containers/podman-machine" {
					assert.Contains(t, f.Content, vmType.String())
					return
				}
			}
			t.Fatal("/etc/containers/podman-machine should be in write_files")
		})
	}
}

func TestCloudInitBuilder_WithUnit(t *testing.T) {
	ci := newTestCloudInit(t)
	require.NoError(t, ci.GenerateCloudInitConfig())

	builder := NewCloudInitBuilder(ci)

	unitContent := "[Unit]\nDescription=Test\n[Service]\nExecStart=/bin/true\n"
	builder.WithUnit(SystemdUnit{
		Name:     "test.service",
		Contents: &unitContent,
		Enabled:  boolToPtr(true),
	})

	require.NoError(t, builder.Build())

	// Unit file should be written
	userData, err := os.ReadFile(filepath.Join(ci.WritePath, "user-data"))
	require.NoError(t, err)
	content := string(userData)
	assert.Contains(t, content, "/etc/systemd/system/test.service")

	// Should have daemon-reload before enable
	assert.Contains(t, content, "daemon-reload")
	assert.Contains(t, content, "systemctl enable --now test.service")
}

func TestCloudInitBuilder_WithUnit_Mask(t *testing.T) {
	ci := newTestCloudInit(t)
	require.NoError(t, ci.GenerateCloudInitConfig())

	builder := NewCloudInitBuilder(ci)
	builder.WithUnit(SystemdUnit{
		Name: "unwanted.service",
		Mask: boolToPtr(true),
	})

	require.NoError(t, builder.Build())

	userData, err := os.ReadFile(filepath.Join(ci.WritePath, "user-data"))
	require.NoError(t, err)
	assert.Contains(t, string(userData), "systemctl mask unwanted.service")
}

func TestCloudInitBuilder_WithUnit_Disable(t *testing.T) {
	ci := newTestCloudInit(t)
	require.NoError(t, ci.GenerateCloudInitConfig())

	builder := NewCloudInitBuilder(ci)
	builder.WithUnit(SystemdUnit{
		Name:    "optional.service",
		Enabled: boolToPtr(false),
	})

	require.NoError(t, builder.Build())

	userData, err := os.ReadFile(filepath.Join(ci.WritePath, "user-data"))
	require.NoError(t, err)
	content := string(userData)
	assert.Contains(t, content, "systemctl disable optional.service || true")
	assert.NotContains(t, content, "enable --now optional.service")
}

func TestCloudInitBuilder_CriticalUnitCommandsExitOnFailure(t *testing.T) {
	ci := newTestCloudInit(t)
	require.NoError(t, ci.GenerateCloudInitConfig())

	builder := NewCloudInitBuilder(ci)

	unitContent := "[Service]\nExecStart=/bin/true\n"
	builder.WithUnit(SystemdUnit{
		Name:     "critical.service",
		Contents: &unitContent,
		Enabled:  boolToPtr(true),
	})
	builder.WithUnit(SystemdUnit{
		Name: "masked.service",
		Mask: boolToPtr(true),
	})

	require.NoError(t, builder.Build())

	// Parse the generated runcmd
	userData, err := os.ReadFile(filepath.Join(ci.WritePath, "user-data"))
	require.NoError(t, err)
	content := string(userData)

	assert.Contains(t, content, "daemon-reload || exit 1")
	assert.Contains(t, content, "enable --now critical.service || exit 1")
	assert.Contains(t, content, "systemctl mask masked.service || exit 1")
}

func TestCloudInitBuilder_WithFile(t *testing.T) {
	ci := newTestCloudInit(t)
	require.NoError(t, ci.GenerateCloudInitConfig())

	builder := NewCloudInitBuilder(ci)
	builder.WithFile(WriteFile{
		Path:    "/etc/custom.conf",
		Content: "custom content",
		Owner:   "root:root",
	})

	require.NoError(t, builder.Build())

	userData, err := os.ReadFile(filepath.Join(ci.WritePath, "user-data"))
	require.NoError(t, err)
	assert.Contains(t, string(userData), "/etc/custom.conf")
	assert.Contains(t, string(userData), "custom content")
}

func TestCloudInitBuilder_BuildWithCloudInitDir(t *testing.T) {
	ci := newTestCloudInit(t)
	builder := NewCloudInitBuilder(ci)

	// Create a source directory with user-data and meta-data
	srcDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "user-data"),
		[]byte("#cloud-config\nusers:\n  - name: custom\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "meta-data"),
		[]byte("instance-id: custom-vm\n"), 0o644))

	require.NoError(t, builder.BuildWithCloudInitDir(srcDir))

	// Files should be copied verbatim
	userData, err := os.ReadFile(filepath.Join(ci.WritePath, "user-data"))
	require.NoError(t, err)
	assert.Equal(t, "#cloud-config\nusers:\n  - name: custom\n", string(userData))

	metaData, err := os.ReadFile(filepath.Join(ci.WritePath, "meta-data"))
	require.NoError(t, err)
	assert.Equal(t, "instance-id: custom-vm\n", string(metaData))
}

func TestCloudInitBuilder_BuildWithCloudInitDir_OptionalMetaData(t *testing.T) {
	ci := newTestCloudInit(t)
	builder := NewCloudInitBuilder(ci)

	// Create a source directory with only user-data (meta-data is optional)
	srcDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "user-data"),
		[]byte("#cloud-config\n"), 0o644))

	require.NoError(t, builder.BuildWithCloudInitDir(srcDir))

	userData, err := os.ReadFile(filepath.Join(ci.WritePath, "user-data"))
	require.NoError(t, err)
	assert.Equal(t, "#cloud-config\n", string(userData))
}

func TestCloudInitBuilder_BuildWithCloudInitDir_MissingUserData(t *testing.T) {
	ci := newTestCloudInit(t)
	builder := NewCloudInitBuilder(ci)

	// Empty source directory — user-data is required
	srcDir := t.TempDir()
	err := builder.BuildWithCloudInitDir(srcDir)
	assert.Error(t, err)
}

func TestCloudInitBuilder_AddPlaybook(t *testing.T) {
	ci := newTestCloudInit(t)
	require.NoError(t, ci.GenerateCloudInitConfig())

	builder := NewCloudInitBuilder(ci)
	require.NoError(t, builder.AddPlaybook(
		"---\n- hosts: localhost\n  tasks: []\n",
		"/home/core/playbook.yml",
		"core",
	))

	require.NoError(t, builder.Build())

	userData, err := os.ReadFile(filepath.Join(ci.WritePath, "user-data"))
	require.NoError(t, err)
	content := string(userData)

	// Playbook file should be written
	assert.Contains(t, content, "/home/core/playbook.yml")
	assert.Contains(t, content, "hosts: localhost")

	// Playbook systemd unit should be created
	assert.Contains(t, content, "playbook.service")
	assert.Contains(t, content, "ansible-playbook /home/core/playbook.yml")
	assert.Contains(t, content, "ConditionFirstBoot=yes")
}

func TestCreateReadyUnitFile_AllProviders(t *testing.T) {
	tests := []struct {
		provider    define.VMType
		opts        *ReadyUnitOpts
		contains    string
		expectError bool
	}{
		{define.QemuVirt, nil, "vport1p1", false},
		{define.AppleHvVirt, nil, "VSOCK-CONNECT:2:1025", false},
		{define.LibKrun, nil, "VSOCK-CONNECT:2:1025", false},
		{define.HyperVVirt, &ReadyUnitOpts{Port: 9999}, "VSOCK-CONNECT:2:9999", false},
		{define.HyperVVirt, nil, "", true},
		{define.HyperVVirt, &ReadyUnitOpts{Port: 0}, "", true},
		{define.WSLVirt, nil, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.provider.String(), func(t *testing.T) {
			content, err := CreateReadyUnitFile(tt.provider, tt.opts)
			if tt.expectError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.provider == define.WSLVirt {
				assert.Empty(t, content)
				return
			}
			assert.Contains(t, content, tt.contains)
			assert.Contains(t, content, "Ready")
		})
	}
}
