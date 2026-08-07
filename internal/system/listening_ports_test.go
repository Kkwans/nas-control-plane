package system

import (
	"context"
	"os"
	"testing"
)

type procFixtureEnvironment struct {
	*networkCapabilityEnvironment
	links map[string]string
}

func (f *procFixtureEnvironment) ReadLink(name string) (string, error) {
	value, ok := f.links[name]
	if !ok {
		return "", os.ErrNotExist
	}
	return value, nil
}

func TestEnrichListeningPortResolvesProcSystemdAndDockerEvidence(t *testing.T) {
	containerID := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	environment := &procFixtureEnvironment{
		networkCapabilityEnvironment: &networkCapabilityEnvironment{
			executables: map[string]bool{"docker": true},
			files: map[string][]byte{
				"/proc/42/comm":    []byte("nginx\n"),
				"/proc/42/cgroup":  []byte("0::/system.slice/docker-" + containerID + ".scope\n"),
				"/proc/42/cmdline": []byte("/usr/sbin/nginx\x00-g\x00"),
			},
			commands: map[string][]byte{
				commandKey("docker", "ps", "--no-trunc", "--format", "{{.ID}}\t{{.Names}}"): []byte(containerID + "\tweb-gateway\n"),
			},
		},
		links: map[string]string{"/proc/42/exe": "/usr/sbin/nginx"},
	}
	port := ListeningPort{Protocol: "tcp", Address: "0.0.0.0", Port: 443, PID: 42}
	enrichListeningPortWithEnvironment(context.Background(), &port, environment)
	ports := []ListeningPort{port}
	enrichListeningPortContainers(context.Background(), environment, ports)
	port = ports[0]

	if port.ProcessName != "nginx" || port.Executable != "/usr/sbin/nginx" {
		t.Fatalf("proc metadata = %#v", port)
	}
	if port.SystemdUnit != "docker-"+containerID+".scope" {
		t.Fatalf("systemd unit = %q", port.SystemdUnit)
	}
	if port.ContainerID != containerID || port.ContainerName != "web-gateway" {
		t.Fatalf("container metadata = %#v", port)
	}
	if port.DetectionStatus != "complete" || !containsString(port.DetectionSources, "docker-cli") {
		t.Fatalf("detection = %#v", port)
	}
}

func TestEnrichListeningPortKeepsShortIDAndReasonWhenDockerNameIsUnknown(t *testing.T) {
	containerID := "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"
	environment := &procFixtureEnvironment{
		networkCapabilityEnvironment: &networkCapabilityEnvironment{
			executables: map[string]bool{"docker": true},
			files: map[string][]byte{
				"/proc/43/comm":   []byte("proxy\n"),
				"/proc/43/cgroup": []byte("0::/system.slice/docker-" + containerID + ".scope\n"),
			},
			commands: map[string][]byte{},
		},
		links: map[string]string{},
	}
	ports := []ListeningPort{{Protocol: "tcp", Address: "127.0.0.1", Port: 9090, PID: 43}}
	enrichListeningPortWithEnvironment(context.Background(), &ports[0], environment)
	enrichListeningPortContainers(context.Background(), environment, ports)
	if ports[0].ContainerName != "" || ports[0].ContainerID != containerID {
		t.Fatalf("unknown container mapping = %#v", ports[0])
	}
	if ports[0].DetectionErrorCode != "LISTENING_PORT_CONTAINER_NAME_UNAVAILABLE" {
		t.Fatalf("unknown mapping reason = %q", ports[0].DetectionErrorCode)
	}
}
