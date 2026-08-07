package system

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestProbeTailscaleRequiresCLIOverlayAndLinkHeartbeat(t *testing.T) {
	environment := &networkCapabilityEnvironment{
		executables: map[string]bool{"tailscale": true},
		commands: map[string][]byte{
			commandKey("tailscale", "status", "--json"): []byte(`{"BackendState":"Running","Version":"1.80.0","Self":{"Online":true,"TailscaleIPs":["100.64.0.2"]}}`),
		},
	}
	down := ProbeTailscale(context.Background(), environment, []InterfaceSnapshot{{
		Name: "tailscale0", State: "down", Addresses: []string{"100.64.0.2"},
	}})
	if down.State != CapabilityStateDegraded || down.Reachable {
		t.Fatalf("down link result = %#v, want degraded and unreachable", down)
	}

	up := ProbeTailscale(context.Background(), environment, []InterfaceSnapshot{{
		Name: "tailscale0", State: "up", Addresses: []string{"100.64.0.2"},
	}})
	if up.State != CapabilityStateAvailable || !up.Reachable || up.OverlayIPs[0] != "100.64.0.2" {
		t.Fatalf("up link result = %#v, want available with overlay address", up)
	}
}

func TestReadInterfaceLinkStateParsesLowerUpFixture(t *testing.T) {
	environment := &networkCapabilityEnvironment{files: map[string][]byte{
		"/sys/class/net/tailscale0/operstate": []byte("unknown\n"),
		"/sys/class/net/tailscale0/flags":     []byte("0x10003\n"),
	}}
	value := readInterfaceLinkState(environment, "tailscale0")
	if value.OperState != "unknown" || !value.LowerUp || !value.LowerUpKnown {
		t.Fatalf("interface link fixture = %#v", value)
	}
}

func TestProbeTailscaleCombinesLowerUpAndOperstate(t *testing.T) {
	environment := &networkCapabilityEnvironment{
		executables: map[string]bool{"tailscale": true},
		commands: map[string][]byte{
			commandKey("tailscale", "status", "--json"): []byte(`{"BackendState":"Running","Self":{"Online":true,"TailscaleIPs":["100.64.0.3"]}}`),
		},
	}

	value := ProbeTailscale(context.Background(), environment, []InterfaceSnapshot{{
		Name: "tailscale0", State: "unknown", LowerUp: true, LowerUpKnown: true,
		Addresses: []string{"100.64.0.3"},
	}})
	if value.LinkState != "up" || !value.Reachable {
		t.Fatalf("LOWER_UP result = %#v, want reachable", value)
	}

	value = ProbeTailscale(context.Background(), environment, []InterfaceSnapshot{{
		Name: "tailscale0", State: "up", LowerUp: false, LowerUpKnown: true,
		Addresses: []string{"100.64.0.3"},
	}})
	if value.LinkState != "down" || value.Reachable {
		t.Fatalf("LOWER_UP down result = %#v, want degraded and unreachable", value)
	}
}

func TestProbeTailscaleUsesContainerCLIStatusAsEvidence(t *testing.T) {
	environment := &networkCapabilityEnvironment{
		executables: map[string]bool{"docker": true},
		commands: map[string][]byte{
			commandKey("docker", "ps", "--no-trunc", "--format", "{{.ID}}\t{{.Names}}\t{{.Image}}"): []byte("0123456789abcdef\ttailscale\ttailscale/tailscale:stable\n"),
			commandKey("docker", "exec", "0123456789abcdef", "tailscale", "status", "--json"):       []byte(`{"BackendState":"Running","Self":{"Online":true,"TailscaleIPs":["100.64.0.4"]}}`),
		},
	}
	value := ProbeTailscale(context.Background(), environment, []InterfaceSnapshot{{
		Name: "tailscale0", State: "up", LowerUp: true, LowerUpKnown: true,
		Addresses: []string{"100.64.0.4"},
	}})
	if !value.Reachable || value.BackendState != "Running" {
		t.Fatalf("container CLI result = %#v", value)
	}
	if !hasCapabilityEvidence(value.Evidence, "docker-cli", "detected") || !hasCapabilityEvidence(value.Evidence, "tailscale-container-cli", "confirmed") {
		t.Fatalf("container CLI evidence = %#v", value.Evidence)
	}
}

func TestProbeTailscaleUsesContainerCLIWhenHostInterfaceIsHidden(t *testing.T) {
	environment := &tailscaleContainerFixtureEnvironment{
		networkCapabilityEnvironment: &networkCapabilityEnvironment{},
		containerEvidence: []TailscaleContainerCLIResult{{
			ContainerID: "0123456789abcdef", ContainerName: "tailscale",
			Status: []byte(`{"BackendState":"Running","Self":{"Online":true,"TailscaleIPs":["100.64.0.5"]}}`),
		}},
	}
	value := ProbeTailscale(context.Background(), environment, nil)
	if !value.Detected || !value.Reachable || value.Interface != "" || value.OverlayIPs[0] != "100.64.0.5" {
		t.Fatalf("container-only result = %#v, want reachable without a host interface", value)
	}
}

func TestProbeTailscaleDoesNotTreatOverlayAsPublicAddress(t *testing.T) {
	for _, address := range []string{"100.64.0.9", "fd7a:115c:a1e0::9", "192.168.1.2", "127.0.0.1"} {
		if isPublicUnicast(parseTestIP(address)) {
			t.Errorf("address %q must not be classified as public egress", address)
		}
	}
	if !isPublicUnicast(parseTestIP("1.1.1.1")) {
		t.Error("1.1.1.1 must be classified as public egress")
	}
}

func TestProbeMihomoOnlyReportsControllerCapabilitiesWithoutSecrets(t *testing.T) {
	environment := &networkCapabilityEnvironment{
		globs: map[string][]string{"/proc/[0-9]*/comm": {"/proc/42/comm"}},
		files: map[string][]byte{"/proc/42/comm": []byte("mihomo\n")},
		httpResults: map[string]HTTPProbeResult{
			"http://127.0.0.1:19090/version": {StatusCode: http.StatusOK, Body: []byte(`{"version":"1.19.12","secret":"must-not-be-returned"}`)},
		},
	}
	value := ProbeMihomo(context.Background(), environment, "http://127.0.0.1:19090")
	if !value.Detected || !value.Controller.Detected || value.Version != "1.19.12" {
		t.Fatalf("mihomo capability = %#v", value)
	}
	if strings.Contains(string(mustMarshal(value)), "must-not-be-returned") {
		t.Fatal("Mihomo probe leaked controller response secret")
	}
	if value.Controller.Endpoint != "http://127.0.0.1:19090" || value.Controller.TokenConfigured {
		t.Fatalf("controller capability leaked or misreported credentials: %#v", value.Controller)
	}
}

func TestProbeMihomoReportsOnlyConfirmedControllerReadCapabilities(t *testing.T) {
	environment := &networkCapabilityEnvironment{
		globs: map[string][]string{"/proc/[0-9]*/comm": {"/proc/42/comm"}},
		files: map[string][]byte{"/proc/42/comm": []byte("mihomo\n")},
		httpResults: map[string]HTTPProbeResult{
			"http://127.0.0.1:19091/version":     {StatusCode: http.StatusOK, Body: []byte(`{"version":"1.0.0"}`)},
			"http://127.0.0.1:19091/connections": {StatusCode: http.StatusOK, Body: []byte(`{"connections":[]}`)},
			"http://127.0.0.1:19091/proxies":     {StatusCode: http.StatusOK, Body: []byte(`{"proxies":{}}`)},
			"http://127.0.0.1:19091/rules":       {StatusCode: http.StatusOK, Body: []byte(`{"rules":[]}`)},
		},
	}
	value := ProbeMihomo(context.Background(), environment, "http://127.0.0.1:19091")
	if !containsString(value.Controller.Operations, string(MihomoOperationConnections)) || !containsString(value.Controller.Operations, string(MihomoOperationProxies)) {
		t.Fatalf("confirmed operations = %#v", value.Controller.Operations)
	}
	if containsString(value.Controller.Operations, string(MihomoOperationSelectProxy)) {
		t.Fatal("a successful GET /proxies must not advertise unverified proxy selection")
	}
	if !hasCapabilityEvidenceDetail(value.Evidence, "controller-api", "/rules") {
		t.Fatalf("rules evidence = %#v", value.Evidence)
	}
	if containsString(value.Controller.Operations, "rules") {
		t.Fatal("rules must not be advertised as a writable operation")
	}
}

func TestProbeDNSMarksStaticResolvConfReadOnly(t *testing.T) {
	environment := &networkCapabilityEnvironment{
		files: map[string][]byte{"/etc/resolv.conf": []byte("nameserver 192.0.2.1\nnameserver 1.1.1.1\n")},
	}
	value := ProbeDNS(context.Background(), environment)
	if value.Backend != DNSBackendStaticResolv || !value.ReadOnly || value.CanPreview || value.CanConfirm || value.CanRollback {
		t.Fatalf("static DNS capability = %#v", value)
	}
	if value.ErrorCode != "DNS_BACKEND_READ_ONLY" || len(value.Nameservers) != 2 {
		t.Fatalf("static DNS capability error/nameservers = %#v", value)
	}

	environment.executables = map[string]bool{"resolvectl": true}
	environment.commands = map[string][]byte{commandKey("resolvectl", "status"): []byte("Global\n")}
	value = ProbeDNS(context.Background(), environment)
	if value.Backend != DNSBackendSystemdResolved || !value.ReadOnly || value.CanPreview || value.CanConfirm || value.CanRollback {
		t.Fatalf("resolved DNS capability = %#v", value)
	}
	if value.ErrorCode != "DNS_WRITE_ADAPTER_UNAVAILABLE" {
		t.Fatalf("resolved DNS error = %q", value.ErrorCode)
	}
}

func TestPublicEgressDetectorReturnsExplicitUnavailableAndRejectsOverlay(t *testing.T) {
	missing := NewPublicEgressDetector("").Detect(context.Background())
	if missing.Status != CapabilityStateUnavailable || missing.ErrorCode != "PUBLIC_EGRESS_ENDPOINT_NOT_CONFIGURED" {
		t.Fatalf("missing endpoint result = %#v", missing)
	}

	environment := &networkCapabilityEnvironment{httpResults: map[string]HTTPProbeResult{
		"https://egress.example.test/ip": {StatusCode: http.StatusOK, Body: []byte(`{"ip":"100.64.0.8"}`)},
	}}
	// The fake transport verifies the same response parsing path without sending a network request.
	detector := NewPublicEgressDetector("https://egress.example.test/ip")
	detector.Client = &http.Client{Transport: fakeRoundTripper{result: environment.httpResults["https://egress.example.test/ip"]}}
	value := detector.Detect(context.Background())
	if value.Status != CapabilityStateUnavailable || value.ErrorCode != "PUBLIC_EGRESS_RESPONSE_INVALID" || value.Address != "" {
		t.Fatalf("overlay egress result = %#v", value)
	}
}

func TestPublicEgressDetectorPreservesCountryISPAndASNMetadata(t *testing.T) {
	detector := NewPublicEgressDetector("https://egress.example.test/ip")
	detector.Client = &http.Client{Transport: fakeRoundTripper{result: HTTPProbeResult{
		StatusCode: http.StatusOK,
		Body:       []byte(`{"ip":"1.1.1.9","country":"CN","region":"Shanghai","isp":"Example ISP","asn":4809}`),
	}}}
	value := detector.Detect(context.Background())
	if value.Status != CapabilityStateAvailable || value.Country != "CN" || value.Region != "Shanghai" || value.ISP != "Example ISP" || value.ASN != "4809" {
		t.Fatalf("egress metadata = %#v", value)
	}
}

func TestPublicEgressDetectorSupportsNestedConnectionMetadata(t *testing.T) {
	detector := NewPublicEgressDetector("https://egress.example.test/ip")
	detector.Client = &http.Client{Transport: fakeRoundTripper{result: HTTPProbeResult{
		StatusCode: http.StatusOK,
		Body:       []byte(`{"ip":"1.1.1.9","country":"CN","region":"Shanghai","connection":{"isp":"Example ISP","asn":4809}}`),
	}}}
	value := detector.Detect(context.Background())
	if value.Status != CapabilityStateAvailable || value.ISP != "Example ISP" || value.ASN != "4809" {
		t.Fatalf("nested egress metadata = %#v", value)
	}
}

func TestListeningPortAlwaysCarriesDetectionSourceWhenPIDUnavailable(t *testing.T) {
	value := ListeningPort{Protocol: "tcp", Address: "0.0.0.0", Port: 8080}
	enrichListeningPort(&value)
	if value.DetectionSource == "" || value.DetectionStatus != "unavailable" || value.DetectionErrorCode != "LISTENING_PORT_PID_UNAVAILABLE" {
		t.Fatalf("port detection = %#v", value)
	}
	association := parseProcessAssociation("0::/system.slice/docker-0123456789abcdef0123456789abcdef.scope\n")
	if association.SystemdUnit == "" || association.ContainerID != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("process association = %#v", association)
	}
}

type networkCapabilityEnvironment struct {
	architecture string
	hostname     string
	files        map[string][]byte
	existing     map[string]bool
	executables  map[string]bool
	commands     map[string][]byte
	globs        map[string][]string
	interfaces   []string
	snapshots    []InterfaceSnapshot
	httpResults  map[string]HTTPProbeResult
}

func (f *networkCapabilityEnvironment) Architecture() string      { return f.architecture }
func (f *networkCapabilityEnvironment) Hostname() (string, error) { return f.hostname, nil }
func (f *networkCapabilityEnvironment) ReadFile(name string) ([]byte, error) {
	value, ok := f.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte{}, value...), nil
}
func (f *networkCapabilityEnvironment) PathExists(name string) bool { return f.existing[name] }
func (f *networkCapabilityEnvironment) LookPath(name string) (string, error) {
	if f.executables[name] {
		return name, nil
	}
	return "", exec.ErrNotFound
}
func (f *networkCapabilityEnvironment) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	value, ok := f.commands[commandKey(name, args...)]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte{}, value...), nil
}
func (f *networkCapabilityEnvironment) Glob(pattern string) ([]string, error) {
	return append([]string{}, f.globs[pattern]...), nil
}
func (f *networkCapabilityEnvironment) NetworkInterfaces() ([]string, error) {
	return append([]string{}, f.interfaces...), nil
}
func (f *networkCapabilityEnvironment) EffectiveUID() int { return 0 }
func (f *networkCapabilityEnvironment) NetworkInterfaceSnapshots(context.Context) ([]InterfaceSnapshot, error) {
	return append([]InterfaceSnapshot{}, f.snapshots...), nil
}
func (f *networkCapabilityEnvironment) HTTPGet(_ context.Context, endpoint string) (HTTPProbeResult, error) {
	value, ok := f.httpResults[endpoint]
	if !ok {
		return HTTPProbeResult{}, os.ErrNotExist
	}
	return value, nil
}

type tailscaleContainerFixtureEnvironment struct {
	*networkCapabilityEnvironment
	containerEvidence []TailscaleContainerCLIResult
}

func (f *tailscaleContainerFixtureEnvironment) TailscaleContainerCLI(context.Context) ([]TailscaleContainerCLIResult, error) {
	return append([]TailscaleContainerCLIResult{}, f.containerEvidence...), nil
}

type fakeRoundTripper struct {
	result HTTPProbeResult
}

func (f fakeRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: f.result.StatusCode,
		Body:       io.NopCloser(strings.NewReader(string(f.result.Body))),
		Header:     make(http.Header),
	}, nil
}

func parseTestIP(value string) net.IP { return net.ParseIP(value) }

func mustMarshal(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func hasCapabilityEvidence(evidence []CapabilityEvidence, source, status string) bool {
	for _, item := range evidence {
		if item.Source == source && item.Status == status {
			return true
		}
	}
	return false
}

func hasCapabilityEvidenceDetail(evidence []CapabilityEvidence, source, detail string) bool {
	for _, item := range evidence {
		if item.Source == source && item.Detail == detail {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
