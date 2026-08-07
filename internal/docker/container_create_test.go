package docker

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestContainerCreateRequestNormalizesAndMapsSupportedFields(t *testing.T) {
	request := ContainerCreateRequest{
		Image:        "alpine:3.21",
		Name:         "demo-web",
		CPU:          1.5,
		MemoryBytes:  64 << 20,
		CPUShares:    512,
		AutoRestart:  true,
		Environment:  map[string]string{"BETA": "two", "ALPHA": "one"},
		Mounts:       []ContainerMount{{Type: "bind", Source: "/volume2/data", Target: "/data", ReadOnly: true}},
		Network:      &ContainerNetwork{Name: "demo-net", IP: "172.30.0.10", Subnet: "172.30.0.0/24"},
		Ports:        []ContainerPort{{ContainerPort: 8080, HostPort: 18080, Protocol: "TCP"}},
		Command:      []string{"/bin/app", "--serve"},
		Privileged:   true,
		CapAdd:       []string{"CAP_NET_ADMIN"},
		CapDrop:      []string{"CAP_SYS_ADMIN"},
		Devices:      []ContainerDevice{{HostPath: "/dev/null", ContainerPath: "/dev/null", CgroupPermissions: "r"}},
		GPUs:         []ContainerGPU{{Driver: "nvidia", Count: 1, Capabilities: []string{"compute"}}},
		RunContainer: true,
	}

	spec, err := request.Normalize()
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if spec.NanoCPUs != 1_500_000_000 || spec.MemoryBytes != 64<<20 || spec.RestartPolicy != "unless-stopped" {
		t.Fatalf("normalized resources = %#v", spec)
	}
	if !reflect.DeepEqual(spec.Environment, []string{"ALPHA=one", "BETA=two"}) {
		t.Fatalf("environment = %#v", spec.Environment)
	}
	if spec.Ports[0].Protocol != "tcp" || spec.CapAdd[0] != "NET_ADMIN" || spec.CapDrop[0] != "SYS_ADMIN" {
		t.Fatalf("normalized aliases = %#v", spec)
	}

	options, err := mapContainerCreateToMoby(spec)
	if err != nil {
		t.Fatalf("mapContainerCreateToMoby() error = %v", err)
	}
	if options.Config == nil || options.Config.Image != "alpine:3.21" || !reflect.DeepEqual([]string(options.Config.Cmd), spec.Command) {
		t.Fatalf("container config = %#v", options.Config)
	}
	if options.HostConfig == nil || options.HostConfig.Resources.NanoCPUs != 1_500_000_000 || options.HostConfig.Resources.Memory != 64<<20 || !options.HostConfig.Privileged {
		t.Fatalf("host config = %#v", options.HostConfig)
	}
	if options.HostConfig.RestartPolicy.Name != "unless-stopped" || len(options.HostConfig.Mounts) != 1 || len(options.HostConfig.DeviceRequests) != 1 {
		t.Fatalf("host config mappings = %#v", options.HostConfig)
	}
	if options.NetworkingConfig == nil || options.NetworkingConfig.EndpointsConfig["demo-net"].IPAMConfig == nil {
		t.Fatalf("networking config = %#v", options.NetworkingConfig)
	}
}

func TestContainerCreatorRejectsInvalidRequestBeforeGatewayCall(t *testing.T) {
	gateway := &fakeContainerCreateGateway{}
	creator := NewContainerCreator(gateway)

	_, err := creator.Create(context.Background(), ContainerCreateRequest{
		Image:   "alpine:3.21",
		Env:     []string{"INVALID NAME=value"},
		Command: []string{"sh", "-c", "echo ok"},
	})
	if ErrorCode(err) != "DOCKER_CONTAINER_CREATE_INVALID" {
		t.Fatalf("error code = %q, want DOCKER_CONTAINER_CREATE_INVALID", ErrorCode(err))
	}
	if gateway.created != 0 {
		t.Fatalf("create calls = %d, want 0", gateway.created)
	}
}

func TestContainerCreatorAcceptsStructuredShellArguments(t *testing.T) {
	request := ContainerCreateRequest{Image: "alpine:3.21", Command: []string{"sh", "-c", "echo $APP_MODE"}}
	spec, err := request.Normalize()
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if !reflect.DeepEqual(spec.Command, request.Command) {
		t.Fatalf("command = %#v", spec.Command)
	}
}

func TestContainerCreateMapsBuiltInNetworkModesWithoutEndpointConfiguration(t *testing.T) {
	for _, networkName := range []string{"host", "none"} {
		t.Run(networkName, func(t *testing.T) {
			spec, err := (ContainerCreateRequest{
				Image:   "alpine:3.21",
				Network: &ContainerNetwork{Name: networkName},
			}).Normalize()
			if err != nil {
				t.Fatalf("Normalize() error = %v", err)
			}
			options, err := mapContainerCreateToMoby(spec)
			if err != nil {
				t.Fatalf("mapContainerCreateToMoby() error = %v", err)
			}
			if string(options.HostConfig.NetworkMode) != networkName || options.NetworkingConfig != nil {
				t.Fatalf("network mapping = mode %q config %#v", options.HostConfig.NetworkMode, options.NetworkingConfig)
			}
		})
	}
}

func TestContainerCreateRejectsCustomSettingsForBuiltInNetworkModes(t *testing.T) {
	_, err := (ContainerCreateRequest{
		Image:   "alpine:3.21",
		Network: &ContainerNetwork{Name: "host", Subnet: "172.30.0.0/24"},
	}).Normalize()
	if ErrorCode(err) != "DOCKER_CONTAINER_CREATE_INVALID" {
		t.Fatalf("error code = %q, want DOCKER_CONTAINER_CREATE_INVALID", ErrorCode(err))
	}
}

func TestContainerCreatorDistinguishesCreateOnlyFromCreateAndStart(t *testing.T) {
	tests := []struct {
		name        string
		run         bool
		wantStarted bool
		wantState   string
	}{
		{name: "create-only", run: false, wantStarted: false, wantState: "stopped"},
		{name: "create-and-start", run: true, wantStarted: true, wantState: "running"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gateway := &fakeContainerCreateGateway{snapshot: ContainerSnapshot{ID: "container-1", Name: "/demo"}, inspected: ContainerSnapshot{ID: "container-1", Name: "/demo", Running: true}}
			result, err := NewContainerCreator(gateway).Create(context.Background(), ContainerCreateRequest{Image: "alpine:3.21", RunContainer: test.run})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if result.Started != test.wantStarted || result.State != test.wantState || result.Created != true {
				t.Fatalf("result = %#v", result)
			}
			if gateway.started != boolToInt(test.run) {
				t.Fatalf("start calls = %d, want %d", gateway.started, boolToInt(test.run))
			}
			if len(gateway.forced) != 0 {
				t.Fatalf("cleanup calls = %#v, want none", gateway.forced)
			}
		})
	}
}

func TestContainerCreatorCancelsWithoutLeavingCreatedObject(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	gateway := &fakeContainerCreateGateway{
		snapshot:    ContainerSnapshot{ID: "container-1", Name: "/demo"},
		afterCreate: cancel,
	}

	_, err := NewContainerCreator(gateway).Create(ctx, ContainerCreateRequest{Image: "alpine:3.21"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if !reflect.DeepEqual(gateway.forced, []string{"container-1"}) {
		t.Fatalf("forced removals = %#v", gateway.forced)
	}
}

func TestContainerControllerKeepsLifecycleOnlyFakeCompatible(t *testing.T) {
	controller := NewContainerController(&lifecycleOnlyGateway{})
	_, err := controller.CreateContainer(context.Background(), ContainerCreateRequest{Image: "alpine:3.21"})
	if ErrorCode(err) != "DOCKER_CONTAINER_CREATE_UNAVAILABLE" {
		t.Fatalf("error code = %q, want DOCKER_CONTAINER_CREATE_UNAVAILABLE", ErrorCode(err))
	}
}

type fakeContainerCreateGateway struct {
	snapshot    ContainerSnapshot
	inspected   ContainerSnapshot
	createErr   error
	startErr    error
	inspectErr  error
	afterCreate func()
	spec        ContainerCreateSpec
	created     int
	started     int
	forced      []string
}

func (f *fakeContainerCreateGateway) CreateContainer(_ context.Context, spec ContainerCreateSpec) (ContainerSnapshot, error) {
	f.created++
	f.spec = spec
	if f.afterCreate != nil {
		f.afterCreate()
	}
	if f.createErr != nil {
		return ContainerSnapshot{}, f.createErr
	}
	return f.snapshot, nil
}

func (f *fakeContainerCreateGateway) StartContainer(context.Context, string) error {
	f.started++
	return f.startErr
}

func (f *fakeContainerCreateGateway) InspectContainer(context.Context, string) (ContainerSnapshot, error) {
	if f.inspectErr != nil {
		return ContainerSnapshot{}, f.inspectErr
	}
	if f.inspected.ID == "" {
		return f.snapshot, nil
	}
	return f.inspected, nil
}

func (f *fakeContainerCreateGateway) ForceRemoveContainer(_ context.Context, id string) error {
	f.forced = append(f.forced, id)
	return nil
}

func (f *fakeContainerCreateGateway) StopContainer(context.Context, string) error { return nil }

func (f *fakeContainerCreateGateway) RemoveContainer(context.Context, string) error { return nil }

func (f *fakeContainerCreateGateway) RestartContainer(context.Context, string) error { return nil }

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type lifecycleOnlyGateway struct{}

func (lifecycleOnlyGateway) StartContainer(context.Context, string) error { return nil }

func (lifecycleOnlyGateway) StopContainer(context.Context, string) error { return nil }

func (lifecycleOnlyGateway) RestartContainer(context.Context, string) error { return nil }

func (lifecycleOnlyGateway) InspectContainer(context.Context, string) (ContainerSnapshot, error) {
	return ContainerSnapshot{}, nil
}
