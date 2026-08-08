package system

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type lsblkDocument struct {
	Devices []lsblkDevice `json:"blockdevices"`
}

type lsblkDevice struct {
	Name     string        `json:"name"`
	Type     string        `json:"type"`
	Size     flexibleUint  `json:"size"`
	Model    *string       `json:"model"`
	Rota     bool          `json:"rota"`
	Tran     *string       `json:"tran"`
	Children []lsblkDevice `json:"children"`
}

type flexibleUint uint64

func (value *flexibleUint) UnmarshalJSON(content []byte) error {
	content = bytes.TrimSpace(content)
	content = bytes.Trim(content, `"`)
	if bytes.Equal(content, []byte("null")) || len(content) == 0 {
		*value = 0
		return nil
	}
	parsed, err := strconv.ParseUint(string(content), 10, 64)
	if err != nil {
		return err
	}
	*value = flexibleUint(parsed)
	return nil
}

func collectBlockDevices(ctx context.Context, environment Environment) ([]PhysicalDiskDetails, error) {
	if environment == nil {
		environment = NewOSEnvironment()
	}
	if _, err := environment.LookPath("lsblk"); err == nil {
		content, runErr := runWithTimeout(ctx, environment, "lsblk", "--json", "--bytes", "--output", "NAME,TYPE,SIZE,MODEL,ROTA,TRAN")
		if runErr == nil {
			devices, parseErr := parseLSBLKDevices(content)
			if parseErr == nil {
				return devices, nil
			}
		}
	}
	devices, err := collectBlockDevicesFromSysfs(environment)
	if err != nil {
		return []PhysicalDiskDetails{}, err
	}
	return devices, nil
}

func parseLSBLKDevices(content []byte) ([]PhysicalDiskDetails, error) {
	var document lsblkDocument
	if err := json.Unmarshal(content, &document); err != nil {
		return nil, fmt.Errorf("parse lsblk: %w", err)
	}
	result := make([]PhysicalDiskDetails, 0, len(document.Devices))
	seen := map[string]struct{}{}
	var visit func([]lsblkDevice)
	visit = func(devices []lsblkDevice) {
		for _, device := range devices {
			if _, ok := seen[device.Name]; !ok {
				if current, include := classifyBlockDevice(device.Name, device.Type, uint64(device.Size), stringValue(device.Model), device.Rota, stringValue(device.Tran)); include {
					result = append(result, current)
					seen[device.Name] = struct{}{}
				}
			}
			visit(device.Children)
		}
	}
	visit(document.Devices)
	sortBlockDevices(result)
	return result, nil
}

func collectBlockDevicesFromSysfs(environment Environment) ([]PhysicalDiskDetails, error) {
	paths, err := environment.Glob("/sys/block/*")
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, errors.New("block devices unavailable")
	}
	result := make([]PhysicalDiskDetails, 0, len(paths))
	for _, path := range paths {
		name := filepath.Base(path)
		sectors, _ := strconv.ParseUint(readEnvironmentTrimmed(environment, filepath.Join(path, "size")), 10, 64)
		device, include := classifyBlockDevice(
			name,
			readEnvironmentTrimmed(environment, filepath.Join(path, "device/type")),
			sectors*512,
			readEnvironmentTrimmed(environment, filepath.Join(path, "device/model")),
			readEnvironmentTrimmed(environment, filepath.Join(path, "queue/rotational")) == "1",
			readEnvironmentTrimmed(environment, filepath.Join(path, "device/transport")),
		)
		if include {
			result = append(result, device)
		}
	}
	sortBlockDevices(result)
	return result, nil
}

func classifyBlockDevice(name, deviceType string, size uint64, model string, rotational bool, transport string) (PhysicalDiskDetails, bool) {
	name = strings.TrimSpace(name)
	deviceType = strings.ToLower(strings.TrimSpace(deviceType))
	transport = strings.ToLower(strings.TrimSpace(transport))
	if name == "" || strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "md") || strings.HasPrefix(deviceType, "raid") || deviceType == "part" {
		return PhysicalDiskDetails{}, false
	}
	result := PhysicalDiskDetails{
		Name: name, Model: strings.TrimSpace(model), SizeBytes: size, Rotational: rotational,
		Kind: "virtual", Role: "virtual", Transport: transport, Health: "unknown",
		Description: "系统虚拟块设备，不是独立物理磁盘",
	}
	switch {
	case strings.HasPrefix(name, "zram"):
		result.Kind = "compressed-memory"
		result.Role = "swap"
		result.Transport = "memory"
		result.Description = "压缩内存交换设备，占用内存而不是物理磁盘空间"
	case isEMMCBootDevice(name):
		result.Kind = "emmc-boot"
		result.Role = "boot"
		result.Transport = "emmc"
		result.Description = "eMMC 固件启动区，由系统引导流程使用，不是数据盘"
	case isEMMCDevice(name):
		result.Kind = "emmc"
		result.Role = "system"
		result.Transport = "emmc"
		result.Description = "NAS 系统 eMMC，用于系统与应用运行"
	case isPhysicalDisk(name, deviceType, transport):
		result.Kind = "physical"
		result.Role = "data"
		if result.Transport == "" {
			result.Transport = inferredTransport(name)
		}
		medium := "固态硬盘"
		if rotational {
			medium = "机械硬盘"
		}
		result.Description = strings.TrimSpace(strings.ToUpper(result.Transport) + " " + medium)
	default:
		if result.Transport == "" {
			result.Transport = "virtual"
		}
	}
	return result, true
}

func isPhysicalDisk(name, deviceType, transport string) bool {
	if deviceType != "" && deviceType != "disk" {
		return false
	}
	if transport == "sata" || transport == "sas" || transport == "usb" || transport == "nvme" || transport == "ata" {
		return true
	}
	return strings.HasPrefix(name, "sd") || strings.HasPrefix(name, "hd") || strings.HasPrefix(name, "nvme")
}

func isEMMCDevice(name string) bool {
	if !strings.HasPrefix(name, "mmcblk") || strings.Contains(name, "boot") {
		return false
	}
	_, err := strconv.Atoi(strings.TrimPrefix(name, "mmcblk"))
	return err == nil
}

func isEMMCBootDevice(name string) bool {
	if !strings.HasPrefix(name, "mmcblk") || !strings.Contains(name, "boot") {
		return false
	}
	parts := strings.SplitN(strings.TrimPrefix(name, "mmcblk"), "boot", 2)
	if len(parts) != 2 {
		return false
	}
	_, leftErr := strconv.Atoi(parts[0])
	_, rightErr := strconv.Atoi(parts[1])
	return leftErr == nil && rightErr == nil
}

func inferredTransport(name string) string {
	if strings.HasPrefix(name, "nvme") {
		return "nvme"
	}
	if strings.HasPrefix(name, "sd") || strings.HasPrefix(name, "hd") {
		return "block"
	}
	return ""
}

func sortBlockDevices(devices []PhysicalDiskDetails) {
	order := map[string]int{"physical": 0, "emmc": 1, "emmc-boot": 2, "compressed-memory": 3, "virtual": 4}
	sort.Slice(devices, func(left, right int) bool {
		leftOrder, rightOrder := order[devices[left].Kind], order[devices[right].Kind]
		if leftOrder != rightOrder {
			return leftOrder < rightOrder
		}
		return devices[left].Name < devices[right].Name
	})
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
