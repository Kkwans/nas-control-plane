package system

import (
	"context"
	"testing"
)

func TestCollectBlockDevicesClassifiesNASStorageWithoutRAIDDuplicates(t *testing.T) {
	environment := &networkCapabilityEnvironment{
		executables: map[string]bool{"lsblk": true},
		commands: map[string][]byte{
			commandKey("lsblk", "--json", "--bytes", "--output", "NAME,TYPE,SIZE,MODEL,ROTA,TRAN"): []byte(`{
        "blockdevices": [
          {"name":"sda","type":"disk","size":8001563222016,"model":"ST8000","rota":true,"tran":"sata","children":[{"name":"sda2","type":"part","size":7985178869760,"rota":true,"children":[{"name":"md2","type":"raid1","size":7985043603456,"rota":true}]}]},
          {"name":"mmcblk0","type":"disk","size":31331450880,"model":null,"rota":false,"tran":null},
          {"name":"mmcblk0boot0","type":"disk","size":4194304,"model":null,"rota":false,"tran":null},
          {"name":"zram0","type":"disk","size":1010827264,"model":null,"rota":false,"tran":null}
        ]
      }`),
		},
	}

	devices, err := collectBlockDevices(context.Background(), environment)
	if err != nil {
		t.Fatalf("collectBlockDevices() error = %v", err)
	}
	if len(devices) != 4 {
		t.Fatalf("devices = %#v, want four classified devices without md2", devices)
	}
	assertBlockDevice(t, devices[0], "sda", "physical", "data", "sata")
	assertBlockDevice(t, devices[1], "mmcblk0", "emmc", "system", "emmc")
	assertBlockDevice(t, devices[2], "mmcblk0boot0", "emmc-boot", "boot", "emmc")
	assertBlockDevice(t, devices[3], "zram0", "compressed-memory", "swap", "memory")
}

func TestCollectBlockDevicesFallsBackToSysfs(t *testing.T) {
	environment := &networkCapabilityEnvironment{
		globs: map[string][]string{"/sys/block/*": {"/sys/block/sdb", "/sys/block/md1"}},
		files: map[string][]byte{
			"/sys/block/sdb/size":             []byte("2048\n"),
			"/sys/block/sdb/device/model":     []byte("Fixture Disk\n"),
			"/sys/block/sdb/queue/rotational": []byte("1\n"),
		},
	}

	devices, err := collectBlockDevices(context.Background(), environment)
	if err != nil {
		t.Fatalf("collectBlockDevices() fallback error = %v", err)
	}
	if len(devices) != 1 || devices[0].Name != "sdb" || devices[0].SizeBytes != 2048*512 {
		t.Fatalf("fallback devices = %#v", devices)
	}
}

func assertBlockDevice(t *testing.T, device PhysicalDiskDetails, name, kind, role, transport string) {
	t.Helper()
	if device.Name != name || device.Kind != kind || device.Role != role || device.Transport != transport || device.Description == "" {
		t.Fatalf("device = %#v, want %s/%s/%s/%s with description", device, name, kind, role, transport)
	}
}
