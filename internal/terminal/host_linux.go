//go:build linux

package terminal

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

type hostStarter struct {
	terminalRCFile string
	bleSHFile      string
}

const ncpTerminalRCFile = "/opt/ncp/etc/terminal.bashrc"
const ncpBleSHFile = "/opt/ncp/share/blesh/ble.sh"
const terminalReadyTimeout = 2 * time.Second

func HostEnhancement() string {
	return hostEnhancement(ncpTerminalRCFile, ncpBleSHFile)
}

func NewHostStarter() Starter {
	return hostStarter{terminalRCFile: ncpTerminalRCFile, bleSHFile: ncpBleSHFile}
}

func (s hostStarter) Start(ctx context.Context, request StartRequest) (Session, error) {
	shell := "/bin/bash"
	arguments := []string{"--noprofile", "--norc", "-i"}
	metadata := SessionMetadata{Shell: "bash", Enhancement: "native"}
	rcFile := s.terminalRCFile
	if rcFile == "" {
		rcFile = ncpTerminalRCFile
	}
	bleFile := s.bleSHFile
	if bleFile == "" {
		bleFile = ncpBleSHFile
	}
	var readyReader *os.File
	var readyWriter *os.File
	var capabilityProbe shellCapabilityProbe
	if _, err := os.Stat(shell); err != nil {
		shell = "/bin/sh"
		arguments = []string{"-i"}
		metadata = hostSessionMetadata("sh", false, false)
	} else if pathExists(rcFile) {
		arguments = []string{"--noprofile", "--rcfile", rcFile, "-i"}
		readyReader, readyWriter, err = os.Pipe()
		if err != nil {
			return nil, coded("TERMINAL_HOST_START_FAILED", err)
		}
		metadata = hostSessionMetadata("bash", true, pathExists(bleFile))
	} else {
		metadata = hostSessionMetadata("bash", false, false)
		capabilityProbe = probeBashCapabilities(ctx, shell, "")
	}
	command := exec.CommandContext(ctx, shell, arguments...)
	command.Dir = "/root"
	locale := supportedTerminalLocale()
	command.Env = []string{
		"HOME=/root",
		"USER=root",
		"LOGNAME=root",
		"SHELL=" + shell,
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"LANG=" + locale,
		"LC_ALL=" + locale,
		"HISTFILE=/root/.ncp_bash_history",
		"HISTCONTROL=ignoredups:erasedups",
		"HISTSIZE=2000",
		"HISTFILESIZE=4000",
		"PS1=\\[\\e[38;5;25m\\]\\u@\\h\\[\\e[0m\\]:\\[\\e[38;5;30m\\]\\w\\[\\e[0m\\]# ",
	}
	if readyWriter != nil {
		command.ExtraFiles = []*os.File{readyWriter}
		command.Env = append(command.Env, "NCP_TERMINAL_READY_FD=3")
	}
	terminalFile, err := pty.StartWithSize(command, &pty.Winsize{Rows: request.Rows, Cols: request.Cols})
	if err != nil {
		if readyReader != nil {
			_ = readyReader.Close()
		}
		if readyWriter != nil {
			_ = readyWriter.Close()
		}
		return nil, coded("TERMINAL_HOST_START_FAILED", err)
	}
	if readyWriter != nil {
		_ = readyWriter.Close()
	}
	if readyReader != nil {
		ready, probe := waitForTerminalReady(readyReader, ctx)
		capabilityProbe = probe
		if ready && probe.BleSH {
			metadata.Enhancement = "blesh"
			metadata.Reason = ""
		} else if metadata.Enhancement == "blesh" {
			metadata.Enhancement = "native"
			if ready {
				metadata.Reason = "ble.sh 未在当前 PTY 中完成加载，已回退原生 Bash"
			} else {
				metadata.Reason = "Shell 增强启动超时，已回退原生 Bash"
			}
		}
	}
	if metadata.Shell == "bash" {
		applyHostBashCapabilities(&metadata, capabilityProbe)
	}
	if err := ctx.Err(); err != nil {
		_ = terminalFile.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, coded("TERMINAL_SESSION_CANCELED", err)
	}
	if err := syscall.SetNonblock(int(terminalFile.Fd()), true); err != nil {
		_ = terminalFile.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, coded("TERMINAL_HOST_START_FAILED", err)
	}

	session := &hostSession{file: terminalFile, command: command, exited: make(chan struct{}), metadata: metadata}
	go func() {
		_ = command.Wait()
		close(session.exited)
	}()
	return session, nil
}

func hostEnhancement(rcFile, bleFile string) string {
	if pathExists(rcFile) && pathExists(bleFile) {
		return "blesh"
	}
	return "native"
}

func hostSessionMetadata(shell string, rcAvailable, bleAvailable bool) SessionMetadata {
	metadata := SessionMetadata{
		Shell:        shell,
		Enhancement:  "native",
		Capabilities: sessionCapabilitiesFor(shell, "native"),
	}
	if shell != "bash" {
		metadata.Reason = "主机未安装 Bash，已回退 /bin/sh"
		return metadata
	}
	if !rcAvailable {
		metadata.Reason = "NCP 专用 Bash 配置不可用，已回退原生 Bash"
		return metadata
	}
	if !bleAvailable {
		metadata.Reason = "ble.sh 不可用，已回退原生 Bash"
		return metadata
	}
	metadata.Enhancement = "blesh"
	return metadata
}

func applyHostBashCapabilities(metadata *SessionMetadata, probe shellCapabilityProbe) {
	if metadata == nil || metadata.Shell != "bash" {
		return
	}
	if metadata.Enhancement == "blesh" && (!probe.BleSH || !probe.Readline) {
		metadata.Enhancement = "native"
		metadata.Reason = "ble.sh 或 readline 未能确认，已回退原生 Bash"
	}
	metadata.Capabilities = sessionCapabilitiesForProbe(metadata.Shell, metadata.Enhancement, probe)
	if !probe.Readline {
		metadata.Reason = appendCapabilityReason(metadata.Reason, "Bash 未确认 readline，补全与历史编辑不可用")
	} else if !probe.BracketedPaste {
		metadata.Reason = appendCapabilityReason(metadata.Reason, "Bash 未启用 bracketed paste，多行粘贴将按行发送")
	}
}

func appendCapabilityReason(reason, addition string) string {
	if reason == "" {
		return addition
	}
	return reason + "；" + addition
}

func pathExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func supportedTerminalLocale() string {
	output, err := exec.Command("locale", "-a").Output()
	if err == nil {
		for _, locale := range strings.Fields(strings.ToLower(string(output))) {
			if locale == "c.utf8" || locale == "c.utf-8" {
				return "C.UTF-8"
			}
		}
	}
	return "C"
}

func waitForTerminalReady(reader *os.File, contexts ...context.Context) (bool, shellCapabilityProbe) {
	defer reader.Close()
	type result struct {
		ready bool
		probe shellCapabilityProbe
	}
	ready := make(chan result, 1)
	go func() {
		output, _ := io.ReadAll(io.LimitReader(reader, 512))
		confirmed, probe := parseTerminalReadySignal(output)
		ready <- result{ready: confirmed, probe: probe}
	}()
	var contextDone <-chan struct{}
	if len(contexts) > 0 && contexts[0] != nil {
		contextDone = contexts[0].Done()
	}
	select {
	case result := <-ready:
		return result.ready, result.probe
	case <-contextDone:
		_ = reader.Close()
		return false, shellCapabilityProbe{Err: context.Canceled}
	case <-time.After(terminalReadyTimeout):
		_ = reader.Close()
		return false, shellCapabilityProbe{Err: context.DeadlineExceeded}
	}
}

type hostSession struct {
	file     *os.File
	command  *exec.Cmd
	exited   chan struct{}
	close    sync.Once
	metadata SessionMetadata
}

func (s *hostSession) Metadata() SessionMetadata {
	metadata := s.metadata
	if metadata.Shell == "" {
		metadata.Shell = filepath.Base(s.command.Path)
	}
	return metadata
}

func (s *hostSession) Read(ctx context.Context, output []byte) (int, error) {
	for {
		read, err := s.file.Read(output)
		if read > 0 || err == nil {
			return read, err
		}
		if !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EWOULDBLOCK) {
			return 0, err
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-s.exited:
			return 0, os.ErrClosed
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (s *hostSession) Write(input []byte) (int, error) {
	return s.file.Write(input)
}

func (s *hostSession) Resize(rows, cols uint16) error {
	return pty.Setsize(s.file, &pty.Winsize{Rows: rows, Cols: cols})
}

func (s *hostSession) Close() error {
	var closeErr error
	s.close.Do(func() {
		if s.command.Process != nil {
			if err := s.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				closeErr = err
			}
		}
		if err := s.file.Close(); err != nil && !errors.Is(err, os.ErrClosed) && closeErr == nil {
			closeErr = err
		}
	})
	return closeErr
}
