package system

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// enrichListeningPort reads only proc metadata for an already discovered socket.
// A failed proc read never removes the port: detectionSource/status preserve the evidence boundary.
func enrichListeningPort(port *ListeningPort) {
	if port == nil {
		return
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
	if value, err := readProcTrimmed(filepath.Join(procRoot, "comm")); err == nil {
		port.ProcessName = value
		metadataRead++
		port.DetectionSources = append(port.DetectionSources, "proc-comm")
	}
	if value, err := os.Readlink(filepath.Join(procRoot, "exe")); err == nil {
		port.Executable = value
		metadataRead++
		port.DetectionSources = append(port.DetectionSources, "proc-exe")
	}
	cgroup, cgroupErr := readProcTrimmed(filepath.Join(procRoot, "cgroup"))
	if cgroupErr == nil {
		metadataRead++
		port.DetectionSources = append(port.DetectionSources, "proc-cgroup")
		association := parseProcessAssociation(cgroup)
		port.SystemdUnit = association.SystemdUnit
		port.ContainerID = association.ContainerID
		port.Service = association.Service
	}
	cmdline, cmdlineErr := readProcBytes(filepath.Join(procRoot, "cmdline"))
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
	value, err := readProcBytes(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.TrimRight(string(value), "\x00")), nil
}

func readProcBytes(path string) ([]byte, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return value, nil
}

func intToString(value int32) string {
	return strconv.FormatInt(int64(value), 10)
}
