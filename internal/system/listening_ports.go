package system

import (
	"context"
	"encoding/hex"
	"path/filepath"
	"strconv"
	"strings"
)

// enrichListeningPort reads only proc metadata for an already discovered socket.
// A failed proc read never removes the port: detectionSource/status preserve the evidence boundary.
func enrichListeningPort(port *ListeningPort) {
	enrichListeningPortWithEnvironment(context.Background(), port, NewOSEnvironment())
}

func enrichListeningPortWithEnvironment(ctx context.Context, port *ListeningPort, environment Environment) {
	if port == nil {
		return
	}
	if environment == nil {
		environment = NewOSEnvironment()
	}
	port.DetectionSources = []string{"gopsutil-connection"}
	port.DetectionSource = "gopsutil-connection"
	port.DetectionStatus = "unavailable"
	if port.PID <= 0 {
		port.DetectionErrorCode = "LISTENING_PORT_PID_UNAVAILABLE"
		return
	}

	procRoot := filepath.Join("/proc", intToString(port.PID))
	metadataRead := 0
	if value, err := readProcTrimmedFrom(environment, filepath.Join(procRoot, "comm")); err == nil {
		port.ProcessName = value
		metadataRead++
		port.DetectionSources = append(port.DetectionSources, "proc-comm")
	}
	if value, err := readProcLinkFrom(environment, filepath.Join(procRoot, "exe")); err == nil {
		port.Executable = value
		metadataRead++
		port.DetectionSources = append(port.DetectionSources, "proc-exe")
	}
	cgroup, cgroupErr := readProcTrimmedFrom(environment, filepath.Join(procRoot, "cgroup"))
	if cgroupErr == nil {
		metadataRead++
		port.DetectionSources = append(port.DetectionSources, "proc-cgroup")
		association := parseProcessAssociation(cgroup)
		port.SystemdUnit = association.SystemdUnit
		port.ContainerID = association.ContainerID
		port.Service = association.Service
	}
	cmdline, cmdlineErr := readProcBytesFrom(environment, filepath.Join(procRoot, "cmdline"))
	if cmdlineErr == nil {
		metadataRead++
		port.DetectionSources = append(port.DetectionSources, "proc-cmdline")
		if port.Service == "" {
			port.Service = processServiceName(string(cmdline), port.ProcessName)
		}
	}

	if metadataRead == 0 {
		port.DetectionStatus = "unavailable"
		port.DetectionErrorCode = "LISTENING_PORT_PROCESS_METADATA_UNAVAILABLE"
		return
	}
	if port.ProcessName == "" && port.Executable == "" && port.SystemdUnit == "" && port.ContainerID == "" && port.Service == "" {
		port.DetectionStatus = "partial"
		port.DetectionErrorCode = "LISTENING_PORT_PROCESS_METADATA_EMPTY"
		return
	}
	if port.ProcessName == "" || port.Executable == "" {
		port.DetectionStatus = "partial"
		port.DetectionErrorCode = "LISTENING_PORT_PROCESS_METADATA_PARTIAL"
		return
	}
	port.DetectionStatus = "complete"
	port.DetectionErrorCode = ""
}

func enrichListeningPortContainers(ctx context.Context, environment Environment, ports []ListeningPort) {
	if environment == nil || len(ports) == 0 {
		return
	}
	containerIDs := make([]string, 0)
	seen := map[string]struct{}{}
	for _, port := range ports {
		if port.ContainerID == "" {
			continue
		}
		if _, ok := seen[port.ContainerID]; ok {
			continue
		}
		seen[port.ContainerID] = struct{}{}
		containerIDs = append(containerIDs, port.ContainerID)
	}
	if len(containerIDs) == 0 {
		return
	}
	if _, err := environment.LookPath("docker"); err != nil {
		markContainerNamesUnavailable(ports)
		return
	}
	output, err := runWithTimeout(ctx, environment, "docker", "ps", "--no-trunc", "--format", "{{.ID}}\t{{.Names}}")
	if err != nil {
		markContainerNamesUnavailable(ports)
		return
	}
	names := parseContainerNames(output)
	for index := range ports {
		if ports[index].ContainerID == "" {
			continue
		}
		name := lookupContainerName(names, ports[index].ContainerID)
		if name == "" {
			markContainerNameUnavailable(&ports[index])
			continue
		}
		ports[index].ContainerName = name
		ports[index].DetectionSources = appendUniqueString(ports[index].DetectionSources, "docker-cli")
	}
}

func parseContainerNames(content []byte) map[string]string {
	result := map[string]string{}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 {
			continue
		}
		id, name := strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1])
		if id != "" && name != "" {
			result[id] = name
		}
	}
	return result
}

func lookupContainerName(names map[string]string, containerID string) string {
	if name := names[containerID]; name != "" {
		return name
	}
	for id, name := range names {
		if strings.HasPrefix(id, containerID) || strings.HasPrefix(containerID, id) {
			return name
		}
	}
	return ""
}

func markContainerNamesUnavailable(ports []ListeningPort) {
	for index := range ports {
		if ports[index].ContainerID != "" {
			markContainerNameUnavailable(&ports[index])
		}
	}
}

func markContainerNameUnavailable(port *ListeningPort) {
	if port == nil || port.ContainerID == "" || port.ContainerName != "" {
		return
	}
	port.DetectionSources = appendUniqueString(port.DetectionSources, "docker-cli")
	port.DetectionErrorCode = "LISTENING_PORT_CONTAINER_NAME_UNAVAILABLE"
}

func appendUniqueString(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

type processAssociation struct {
	SystemdUnit string
	ContainerID string
	Service     string
}

func parseProcessAssociation(cgroup string) processAssociation {
	result := processAssociation{}
	for _, line := range strings.Split(cgroup, "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), ":", 3)
		if len(fields) != 3 {
			continue
		}
		path := fields[2]
		for _, segment := range strings.Split(path, "/") {
			segment = strings.TrimSpace(segment)
			if strings.HasSuffix(segment, ".service") || strings.HasSuffix(segment, ".scope") {
				if result.SystemdUnit == "" || strings.HasSuffix(segment, ".service") {
					result.SystemdUnit = segment
				}
			}
			if candidate := containerIDFromSegment(segment); candidate != "" {
				result.ContainerID = candidate
			}
		}
	}
	if result.SystemdUnit != "" {
		result.Service = strings.TrimSuffix(strings.TrimSuffix(result.SystemdUnit, ".service"), ".scope")
	}
	return result
}

func containerIDFromSegment(segment string) string {
	segment = strings.TrimSuffix(segment, ".scope")
	for _, prefix := range []string{"docker-", "libpod-", "crio-", "containerd-"} {
		segment = strings.TrimPrefix(segment, prefix)
	}
	for _, separator := range []string{"docker/", "libpod/", "crio/", "containerd/"} {
		segment = strings.TrimPrefix(segment, separator)
	}
	if len(segment) < 12 || len(segment) > 128 {
		return ""
	}
	if _, err := hex.DecodeString(segment); err != nil {
		return ""
	}
	return segment
}

func processServiceName(cmdline, processName string) string {
	parts := strings.FieldsFunc(cmdline, func(r rune) bool { return r == 0 || r == ' ' || r == '\t' })
	if len(parts) == 0 {
		return processName
	}
	base := filepath.Base(parts[0])
	if base == "" || base == "." || base == "/" {
		return processName
	}
	return base
}

func readProcTrimmed(path string) (string, error) {
	return readProcTrimmedFrom(NewOSEnvironment(), path)
}

func readProcTrimmedFrom(environment Environment, path string) (string, error) {
	value, err := readProcBytesFrom(environment, path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.TrimRight(string(value), "\x00")), nil
}

func readProcBytes(path string) ([]byte, error) {
	return readProcBytesFrom(NewOSEnvironment(), path)
}

func readProcBytesFrom(environment Environment, path string) ([]byte, error) {
	if environment == nil {
		return nil, filepath.ErrBadPattern
	}
	return environment.ReadFile(path)
}

func readProcLinkFrom(environment Environment, path string) (string, error) {
	if source, ok := environment.(readLinkEnvironment); ok {
		return source.ReadLink(path)
	}
	return "", filepath.ErrBadPattern
}

func intToString(value int32) string {
	return strconv.FormatInt(int64(value), 10)
}
