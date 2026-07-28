//go:build linux

package terminal

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

type hostStarter struct{}

const ncpTerminalRCFile = "/opt/ncp/etc/terminal.bashrc"
const ncpBleSHFile = "/opt/ncp/share/blesh/ble.sh"

func HostEnhancement() string {
	if _, err := os.Stat(ncpTerminalRCFile); err == nil {
		if _, err := os.Stat(ncpBleSHFile); err == nil {
			return "blesh"
		}
	}
	return "native"
}

func NewHostStarter() Starter {
	return hostStarter{}
}

func (hostStarter) Start(ctx context.Context, request StartRequest) (Session, error) {
	shell := "/bin/bash"
	arguments := []string{"--noprofile", "--norc", "-i"}
	if _, err := os.Stat(shell); err != nil {
		shell = "/bin/sh"
		arguments = []string{"-i"}
	} else if _, err := os.Stat(ncpTerminalRCFile); err == nil {
		arguments = []string{"--noprofile", "--rcfile", ncpTerminalRCFile, "-i"}
	}
	command := exec.CommandContext(ctx, shell, arguments...)
	command.Dir = "/root"
	command.Env = []string{
		"HOME=/root",
		"USER=root",
		"LOGNAME=root",
		"SHELL=" + shell,
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"HISTFILE=/root/.ncp_bash_history",
		"HISTCONTROL=ignoredups:erasedups",
		"HISTSIZE=2000",
		"HISTFILESIZE=4000",
		"PS1=\\[\\e[38;5;25m\\]\\u@\\h\\[\\e[0m\\]:\\[\\e[38;5;30m\\]\\w\\[\\e[0m\\]# ",
	}
	terminalFile, err := pty.StartWithSize(command, &pty.Winsize{Rows: request.Rows, Cols: request.Cols})
	if err != nil {
		return nil, coded("TERMINAL_HOST_START_FAILED", err)
	}
	if err := syscall.SetNonblock(int(terminalFile.Fd()), true); err != nil {
		_ = terminalFile.Close()
		_ = command.Process.Kill()
		return nil, coded("TERMINAL_HOST_START_FAILED", err)
	}

	session := &hostSession{file: terminalFile, command: command, exited: make(chan struct{})}
	go func() {
		_ = command.Wait()
		close(session.exited)
	}()
	return session, nil
}

type hostSession struct {
	file    *os.File
	command *exec.Cmd
	exited  chan struct{}
	close   sync.Once
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
