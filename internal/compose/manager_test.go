package compose

import (
	"context"
	"strings"
	"testing"
)

type fakeRunner struct {
	output string
	err    error
}

func (runner fakeRunner) Validate(context.Context, string, string) (string, error) {
	return runner.output, runner.err
}

func (runner fakeRunner) Deploy(context.Context, string, []string) (string, error) {
	return runner.output, runner.err
}

func TestValidateExtractsComposeServices(t *testing.T) {
	manager := NewManager(fakeRunner{output: "name: demo\nservices:\n  api:\n    image: api:latest\n  web:\n    image: web:latest\nnetworks:\n  default:\n"})
	result, err := manager.Validate(context.Background(), ValidateRequest{
		Path:    "/volume2/Project/demo/compose.yaml",
		Content: "services:\n  api:\n    image: api:latest\n",
	})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !result.Valid || strings.Join(result.Services, ",") != "api,web" {
		t.Fatalf("result = %#v", result)
	}
}

func TestValidateRejectsConfigOutsideProjectVolumes(t *testing.T) {
	manager := NewManager(fakeRunner{})
	if _, err := manager.Validate(context.Background(), ValidateRequest{Path: "/etc/compose.yaml", Content: "services: {}"}); err == nil {
		t.Fatal("Validate() error = nil, want path rejection")
	}
}

func TestReadRejectsConfigOutsideWorkingDirectory(t *testing.T) {
	manager := NewManager(fakeRunner{})
	_, err := manager.Read(context.Background(), ReadRequest{
		ProjectID: "compose:demo", WorkingDirectory: "/volume2/Project/demo",
		ConfigFiles: []string{"/volume2/Project/other/compose.yaml"},
	})
	if err == nil {
		t.Fatal("Read() error = nil, want path rejection")
	}
}
