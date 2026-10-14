package docker

import "testing"

// 仅验证内存中的规范化和 Moby 请求结构，不打开或创建设备。
func TestContainerDevicePreservesPathIdentityThroughMoby(t *testing.T) {
	for _, suffix := range []string{"", " ", "\t", "\u00a0", "\u3000"} {
		host, target := "/dev/selected"+suffix, "/dev/target"+suffix
		input := ContainerCreateRequest{Image: "fixture:latest", Devices: []ContainerDevice{{HostPath: host, ContainerPath: target, CgroupPermissions: " Rw "}}}
		spec, err := input.Normalize()
		if err != nil {
			t.Fatal(err)
		}
		mapped, err := mapContainerCreateToMoby(spec)
		if err != nil {
			t.Fatal(err)
		}
		got := mapped.HostConfig.Devices[0]
		if got.PathOnHost != host || got.PathInContainer != target || got.CgroupPermissions != "rw" {
			t.Fatalf("input (%q, %q), mapped %#v", host, target, got)
		}
		if input.Devices[0].CgroupPermissions != " Rw " {
			t.Fatal("input mutated")
		}
	}
}

func TestContainerDeviceRejectsInvalidRawPaths(t *testing.T) {
	for _, invalid := range []string{"", " ", " /dev/a", "\t/dev/a", "\u00a0/dev/a", "\u3000/dev/a", "dev/a", "/dev/../a", "/dev//a", "/dev/a/", "/dev/a\x00", "/dev/a\r", "/dev/a\n", "/dev/a:b"} {
		for _, device := range []ContainerDevice{{HostPath: invalid, ContainerPath: "/dev/b"}, {HostPath: "/dev/a", ContainerPath: invalid}} {
			if _, err := normalizeDevices([]ContainerDevice{device}); ErrorCode(err) != "DOCKER_CONTAINER_CREATE_INVALID" {
				t.Errorf("device %#v: %v", device, err)
			}
		}
	}
}

func TestContainerDeviceDistinguishesWhitespaceTargets(t *testing.T) {
	values := []ContainerDevice{}
	for _, suffix := range []string{"", " ", "\t", "\u00a0", "\u3000"} {
		values = append(values, ContainerDevice{HostPath: "/dev/source" + suffix, ContainerPath: "/dev/target" + suffix})
	}
	got, err := normalizeDevices(values)
	if err != nil || len(got) != len(values) {
		t.Fatalf("got %#v, error %v", got, err)
	}
	for _, device := range values {
		if _, err := normalizeDevices([]ContainerDevice{device, device}); ErrorCode(err) != "DOCKER_CONTAINER_CREATE_INVALID" {
			t.Errorf("duplicate %#v: %v", device, err)
		}
	}
}
