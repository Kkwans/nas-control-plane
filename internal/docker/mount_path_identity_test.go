package docker

import (
	"testing"
)

func TestContainerMountPreservesBindPathThroughMobyMapping(t *testing.T) {
	for _, suffix := range []string{"", " ", "\t", "\u00a0", "\u3000"} {
		source, target := "/selected"+suffix, "/data"+suffix
		request := ContainerCreateRequest{Image: "alpine:latest", Mounts: []ContainerMount{{Type: "bind", Source: source, Target: target}}}
		spec, err := request.Normalize()
		if err != nil {
			t.Fatal(err)
		}
		mapped, err := mapContainerCreateToMoby(spec)
		if err != nil {
			t.Fatal(err)
		}
		got := mapped.HostConfig.Mounts[0]
		if got.Source != source || got.Target != target {
			t.Errorf("requested (%q, %q), mapped (%q, %q)", source, target, got.Source, got.Target)
		}
	}
}

func TestContainerMountPathFixRejectsLeadingWhitespaceForAllMountTypes(t *testing.T) {
	for _, prefix := range []string{" ", "\t", "\u00a0", "\u3000"} {
		for _, value := range []ContainerMount{
			{Type: "bind", Source: prefix + "/source", Target: "/data"},
			{Type: "bind", Source: "/source", Target: prefix + "/data"},
			{Type: "volume", Source: "app-data", Target: prefix + "/data"},
			{Type: "tmpfs", Target: prefix + "/data"},
		} {
			if _, err := normalizeMounts([]ContainerMount{value}); ErrorCode(err) != "DOCKER_CONTAINER_CREATE_INVALID" {
				t.Errorf("normalizeMounts(%#v) error = %v", value, err)
			}
		}
	}
}

func TestContainerMountPathFixRejectsDuplicateExactTargets(t *testing.T) {
	for _, suffix := range []string{"", " ", "\t", "\u00a0", "\u3000"} {
		values := []ContainerMount{
			{Type: "bind", Source: "/source", Target: "/data" + suffix},
			{Type: "volume", Source: "app-data", Target: "/data" + suffix},
		}
		if _, err := normalizeMounts(values); ErrorCode(err) != "DOCKER_CONTAINER_CREATE_INVALID" {
			t.Errorf("normalizeMounts(%#v) error = %v", values, err)
		}
	}
}

func TestContainerMountPathFixPreservesVolumeAndTmpfsNormalization(t *testing.T) {
	input := []ContainerMount{
		{Type: " VOLUME ", Source: " app-data ", Target: "/volume "},
		{Type: " tmpfs ", Source: " \t", Target: "/temp "},
		{Type: "bind", Source: "/source", Target: "/data"},
		{Type: "bind", Source: "/source ", Target: "/data "},
	}
	result, err := normalizeMounts(input)
	if err != nil {
		t.Fatal(err)
	}
	if result[0].Type != "volume" || result[0].Source != "app-data" || result[0].Target != "/volume " || result[1].Type != "tmpfs" || result[1].Source != "" || result[1].Target != "/temp " {
		t.Fatalf("mounts = %#v", result)
	}
	if result[2].Target == result[3].Target || result[2].Source == result[3].Source {
		t.Fatalf("distinct path identities collapsed: %#v", result)
	}
}

func TestContainerMountPathFixKeepsExistingRejections(t *testing.T) {
	for _, invalid := range []ContainerMount{
		{Type: "bind", Source: "", Target: "/data"},
		{Type: "bind", Source: " \t", Target: "/data"},
		{Type: "bind", Source: " /source", Target: "/data"},
		{Type: "bind", Source: "/source", Target: " /data"},
		{Type: "bind", Source: "/source", Target: " \t"},
		{Type: "bind", Source: "/source/../data", Target: "/data"},
		{Type: "bind", Source: "/source\n", Target: "/data"},
		{Type: "bind", Source: "/source:other", Target: "/data"},
		{Type: "bind", Source: "/source\x00", Target: "/data"},
		{Type: "volume", Source: " \t", Target: "/data"},
		{Type: "volume", Source: "bad name", Target: "/data"},
		{Type: "tmpfs", Source: "unexpected", Target: "/data"},
	} {
		if _, err := normalizeMounts([]ContainerMount{invalid}); ErrorCode(err) != "DOCKER_CONTAINER_CREATE_INVALID" {
			t.Errorf("normalizeMounts(%#v) error = %v", invalid, err)
		}
	}
}
