package system

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestCollectReportsARM64NASCapabilities(t *testing.T) {
	t.Parallel()

	env := completeEnvironment()
	capabilities, err := NewProbe(env).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if capabilities.Architecture != "arm64" {
		t.Errorf("Architecture = %q, want arm64", capabilities.Architecture)
	}
	if capabilities.OperatingSystem.PrettyName != "Debian GNU/Linux 12 (bookworm)" {
		t.Errorf("OperatingSystem.PrettyName = %q", capabilities.OperatingSystem.PrettyName)
	}
	if capabilities.DeviceModel == nil || *capabilities.DeviceModel != "UGREEN DH4300 Plus" {
		t.Errorf("DeviceModel = %v, want UGREEN DH4300 Plus", capabilities.DeviceModel)
	}
	if !capabilities.Docker || valueOf(capabilities.DockerAPIVersion) != "1.54" {
		t.Errorf("Docker = %t, DockerAPIVersion = %q", capabilities.Docker, valueOf(capabilities.DockerAPIVersion))
	}
	if !capabilities.Compose || valueOf(capabilities.ComposeVersion) != "5.1.3" {
		t.Errorf("Compose = %t, ComposeVersion = %q", capabilities.Compose, valueOf(capabilities.ComposeVersion))
	}
	if !capabilities.Systemd || !capabilities.Journald || capabilities.CgroupVersion != 2 {
		t.Errorf("systemd=%t journald=%t cgroup=%d", capabilities.Systemd, capabilities.Journald, capabilities.CgroupVersion)
	}
	if !capabilities.ProcReadable || !capabilities.SysReadable {
		t.Errorf("procReadable=%t sysReadable=%t", capabilities.ProcReadable, capabilities.SysReadable)
	}
	if !capabilities.Smartctl || capabilities.NvmeCLI {
		t.Errorf("smartctl=%t nvmeCli=%t", capabilities.Smartctl, capabilities.NvmeCLI)
	}
	if !sameStrings(capabilities.TemperatureSensors, []string{"thermal_zone0", "thermal_zone1"}) {
		t.Errorf("TemperatureSensors = %#v", capabilities.TemperatureSensors)
	}
	if !sameStrings(capabilities.DataVolumes, []string{"/volume1", "/volume2"}) {
		t.Errorf("DataVolumes = %#v", capabilities.DataVolumes)
	}
	if !sameStrings(capabilities.NetworkInterfaces, []string{"eth0", "lo"}) {
		t.Errorf("NetworkInterfaces = %#v", capabilities.NetworkInterfaces)
	}
	if !capabilities.CanManageSystemUsers || !capabilities.RootFilesystemWritable || !capabilities.HostTerminal {
		t.Errorf("userManagement=%t rootWritable=%t hostTerminal=%t", capabilities.CanManageSystemUsers, capabilities.RootFilesystemWritable, capabilities.HostTerminal)
	}
	if len(capabilities.Warnings) != 0 {
		t.Errorf("Warnings = %#v, want none", capabilities.Warnings)
	}
	if len(env.commandsRun) != 2 {
		t.Fatalf("command count = %d, want 2", len(env.commandsRun))
	}
	for _, command := range env.commandsRun {
		if !command.hasDeadline {
			t.Errorf("command %q was executed without a deadline", command.name)
		}
	}
}

func TestCollectDegradesWhenOptionalCapabilitiesAreMissing(t *testing.T) {
	t.Parallel()

	env := &fakeEnvironment{
		architecture: "arm64",
		files: map[string]string{
			"/proc/mounts": "overlay / overlay ro,relatime 0 0\n",
		},
	}

	capabilities, err := NewProbe(env).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if capabilities.Docker || capabilities.Compose || capabilities.Systemd || capabilities.Journald {
		t.Errorf("missing optional capabilities must remain false: %#v", capabilities)
	}
	if capabilities.CgroupVersion != 0 {
		t.Errorf("CgroupVersion = %d, want 0", capabilities.CgroupVersion)
	}
	if capabilities.RootFilesystemWritable {
		t.Error("RootFilesystemWritable = true, want false for a read-only root mount")
	}
	if capabilities.DockerAPIVersion != nil || capabilities.ComposeVersion != nil || capabilities.DeviceModel != nil {
		t.Errorf("optional versions/model should be nil: %#v", capabilities)
	}
	if !hasWarning(capabilities.Warnings, "PROBE_SOURCE_UNAVAILABLE", "/etc/os-release") {
		t.Errorf("Warnings = %#v, want missing os-release warning", capabilities.Warnings)
	}
}

func TestCollectStopsWhenTheCallerContextIsCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewProbe(completeEnvironment()).Collect(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Collect() error = %v, want context.Canceled", err)
	}
}

func TestCollectRecognizesCgroupV1WhenTheV2ControllerIsAbsent(t *testing.T) {
	t.Parallel()

	env := completeEnvironment()
	delete(env.existing, "/sys/fs/cgroup/cgroup.controllers")
	env.files["/proc/cgroups"] = "#subsys_name\thierarchy\tnum_cgroups\tenabled\ncpuset\t2\t1\t1\n"

	capabilities, err := NewProbe(env).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if capabilities.CgroupVersion != 1 {
		t.Errorf("CgroupVersion = %d, want 1", capabilities.CgroupVersion)
	}
}

func TestCollectReturnsOnlyDataVolumeRoots(t *testing.T) {
	t.Parallel()

	env := completeEnvironment()
	env.files["/proc/mounts"] = "overlay / overlay rw,relatime 0 0\n/dev/sda1 /volume1 ext4 rw,relatime 0 0\n/dev/sdb1 /volume2 ext4 rw,relatime 0 0\noverlay /volume1/@docker/overlay2/example/merged overlay rw,relatime 0 0\n"

	capabilities, err := NewProbe(env).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if !sameStrings(capabilities.DataVolumes, []string{"/volume1", "/volume2"}) {
		t.Errorf("DataVolumes = %#v, want only data volume roots", capabilities.DataVolumes)
	}
}

func completeEnvironment() *fakeEnvironment {
	return &fakeEnvironment{
		architecture: "arm64",
		hostname:     "DH4300Plus",
		files: map[string]string{
			"/etc/os-release":           "ID=debian\nVERSION_ID=12\nPRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\n",
			"/proc/cpuinfo":             "processor\t: 0\n",
			"/sys/kernel/uevent_seqnum": "42\n",
			"/proc/device-tree/model":   "UGREEN DH4300 Plus\x00",
			"/proc/mounts":              "overlay / overlay rw,relatime 0 0\n/dev/sda1 /volume1 ext4 rw,relatime 0 0\n/dev/sdb1 /volume2 ext4 rw,relatime 0 0\n",
		},
		existing: map[string]bool{
			"/var/run/docker.sock":              true,
			"/run/systemd/system":               true,
			"/sys/fs/cgroup/cgroup.controllers": true,
			"/dev/ptmx":                         true,
			"/bin/bash":                         true,
		},
		executables: map[string]bool{
			"docker":     true,
			"journalctl": true,
			"smartctl":   true,
			"useradd":    true,
		},
		commands: map[string]commandResult{
			commandKey("docker", "version", "--format", "{{.Server.APIVersion}}"): {output: "1.54\n"},
			commandKey("docker", "compose", "version", "--short"):                 {output: "5.1.3\n"},
		},
		globs: map[string][]string{
			"/sys/class/thermal/thermal_zone*/temp": {
				"/sys/class/thermal/thermal_zone1/temp",
				"/sys/class/thermal/thermal_zone0/temp",
			},
			"/sys/class/hwmon/hwmon*/temp*_input": nil,
		},
		interfaces: []string{"lo", "eth0"},
		uid:        0,
	}
}

type fakeEnvironment struct {
	architecture string
	hostname     string
	files        map[string]string
	existing     map[string]bool
	executables  map[string]bool
	commands     map[string]commandResult
	globs        map[string][]string
	interfaces   []string
	uid          int
	commandsRun  []observedCommand
}

type commandResult struct {
	output string
	err    error
}

type observedCommand struct {
	name        string
	hasDeadline bool
}

func (f *fakeEnvironment) Architecture() string {
	return f.architecture
}

func (f *fakeEnvironment) Hostname() (string, error) {
	return f.hostname, nil
}

func (f *fakeEnvironment) ReadFile(name string) ([]byte, error) {
	value, ok := f.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return []byte(value), nil
}

func (f *fakeEnvironment) PathExists(name string) bool {
	return f.existing[name]
}

func (f *fakeEnvironment) LookPath(name string) (string, error) {
	if f.executables[name] {
		return name, nil
	}
	return "", exec.ErrNotFound
}

func (f *fakeEnvironment) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	_, hasDeadline := ctx.Deadline()
	f.commandsRun = append(f.commandsRun, observedCommand{name: commandKey(name, args...), hasDeadline: hasDeadline})

	result, ok := f.commands[commandKey(name, args...)]
	if !ok {
		return nil, os.ErrNotExist
	}
	return []byte(result.output), result.err
}

func (f *fakeEnvironment) Glob(pattern string) ([]string, error) {
	return append([]string(nil), f.globs[pattern]...), nil
}

func (f *fakeEnvironment) NetworkInterfaces() ([]string, error) {
	return append([]string(nil), f.interfaces...), nil
}

func (f *fakeEnvironment) EffectiveUID() int {
	return f.uid
}

func commandKey(name string, args ...string) string {
	return strings.Join(append([]string{name}, args...), "\x00")
}

func valueOf(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func hasWarning(warnings []ProbeWarning, code, source string) bool {
	for _, warning := range warnings {
		if warning.Code == code && warning.Source == source {
			return true
		}
	}
	return false
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
