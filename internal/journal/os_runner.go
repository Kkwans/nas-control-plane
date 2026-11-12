package journal

import (
	"context"
	"errors"
	"io"
	"os/exec"
)

// OSRunner executes only fixed, validated journalctl argument lists assembled
// by Reader. It deliberately exposes no generic command execution API.
type OSRunner struct{}

func (OSRunner) Output(ctx context.Context, args ...string) ([]byte, error) {
	return queryOutput(ctx, args, executeQuery)
}

// 执行器按调用注入，测试无需启动进程或修改全局钩子。返回前必须等待命令结束。
type queryExecutor func(context.Context, io.Writer, ...string) error

func executeQuery(ctx context.Context, stdout io.Writer, args ...string) error {
	command := exec.CommandContext(ctx, "journalctl", args...)
	command.Stdout = stdout
	command.Stderr = io.Discard
	return command.Run()
}

func queryOutput(ctx context.Context, args []string, execute queryExecutor) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	commandContext, cancel := context.WithCancel(ctx)
	defer cancel()
	output := newQueryOutputCollector(cancel)
	err := execute(commandContext, output, args...)
	if callerErr := ctx.Err(); callerErr != nil {
		return nil, callerErr
	}
	// 溢出优先于退出码 1，禁止将超预算结果当成成功或空页。
	if output.overflow {
		return nil, errQueryOutputBudget
	}
	if isNoEntriesExit(err) {
		return output.data, nil
	}
	return output.data, err
}

var errQueryOutputBudget = errors.New("journal query output exceeds byte budget")

// 固定容量缓冲只接收历史查询原始输出；溢出后不再写入，也不返回截断页。
// exec.Cmd 在单个 stdout 复制协程中写入，Run 返回后才读取其状态。
type queryOutputCollector struct {
	data     []byte
	overflow bool
	cancel   context.CancelFunc
}

func newQueryOutputCollector(cancel context.CancelFunc) *queryOutputCollector {
	return &queryOutputCollector{data: make([]byte, 0, maximumQueryOutputSize), cancel: cancel}
}

func (output *queryOutputCollector) Write(chunk []byte) (int, error) {
	if output.overflow {
		return 0, errQueryOutputBudget
	}
	if len(chunk) > maximumQueryOutputSize-len(output.data) {
		output.overflow = true
		output.cancel()
		return 0, errQueryOutputBudget
	}
	output.data = append(output.data, chunk...)
	return len(chunk), nil
}

func (OSRunner) Follow(ctx context.Context, args ...string) (io.ReadCloser, <-chan error, error) {
	command := exec.CommandContext(ctx, "journalctl", args...)
	command.Stderr = io.Discard
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := command.Start(); err != nil {
		return nil, nil, err
	}
	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
		close(done)
	}()
	return output, done, nil
}

func isNoEntriesExit(err error) bool {
	if err == nil {
		return false
	}
	var exitCoder interface{ ExitCode() int }
	return errors.As(err, &exitCoder) && exitCoder.ExitCode() == 1
}
