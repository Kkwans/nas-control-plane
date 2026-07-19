package docker

import (
	"context"
	"testing"
)

func TestProtectedExecOptionsAreFixedAndNonPrivileged(t *testing.T) {
	options := protectedExecOptions()

	if options.Privileged {
		t.Fatal("PoC exec 不得使用 privileged 模式")
	}
	if options.AttachStdin || options.TTY {
		t.Fatal("PoC exec 不得开启交互输入或 TTY")
	}
	if !options.AttachStdout || !options.AttachStderr {
		t.Fatal("PoC exec 应仅采集标准输出与标准错误")
	}
	want := []string{"/bin/sh", "-c", "printf NCP_P0_EXEC_OK"}
	if len(options.Cmd) != len(want) {
		t.Fatalf("固定 exec 参数数量 = %d，期望 %d", len(options.Cmd), len(want))
	}
	for index, value := range want {
		if options.Cmd[index] != value {
			t.Fatalf("固定 exec 参数[%d] = %q，期望 %q", index, options.Cmd[index], value)
		}
	}
}

func TestMobyGatewayRejectsImageOutsidePOCScope(t *testing.T) {
	gateway := &mobyGateway{}

	completed, err := gateway.PullImage(context.Background(), "docker.io/library/alpine:latest")
	if err == nil {
		t.Fatal("应拒绝 PoC 固定镜像以外的拉取请求")
	}
	if completed {
		t.Fatal("越界镜像不得报告为拉取完成")
	}
}
