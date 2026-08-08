package docker

import (
	"reflect"
	"testing"
)

func TestSafeHostSitePortsUsesOnlyExplicitPortSignals(t *testing.T) {
	ports := safeHostSitePorts(
		[]string{"PORT=3000", "ADMIN_PORT=3001", "DATABASE_URL=postgres://secret@db:5432/app", "TOKEN=8088-secret"},
		[]string{"docker-entrypoint.sh"},
		[]string{"java", "-jar", "/app.jar", "--server.port=8080", "--password", "9099"},
		[]string{"8443/tcp"},
	)
	want := []int{3000, 3001, 8080, 8443}
	if !reflect.DeepEqual(ports, want) {
		t.Fatalf("ports = %#v, want %#v", ports, want)
	}
}
