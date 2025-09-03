//go:build darwin || freebsd || openbsd || netbsd
// +build darwin freebsd openbsd netbsd

// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package agent provides the agent functionality for the CROWler.
package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// This is the implementation of executeIsolatedCommand for Unix-like systems
// like macOS, BSD etc.

// executeIsolatedCommand (macOS/BSD)
// - UID/GID drop if requested (requires privilege)
// - Captures stdout/stderr + exit code
// - Timeout with process-group kill
// - chroot not supported here via child-only SysProcAttr; reject if requested.
func executeIsolatedCommand(
	command string,
	args []string,
	chrootDir string,
	uid, gid uint32,
	timeout time.Duration,
) (stdout string, stderr string, exitCode int, err error) {
	if command == "" {
		return "", "", -1, fmt.Errorf("empty command")
	}
	if chrootDir != "" {
		return "", "", -1, fmt.Errorf("chroot is not supported on this platform via child-only SysProcAttr")
	}

	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, command, args...)

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	cmd.Env = []string{"PATH=/usr/bin:/bin"}

	sys := &syscall.SysProcAttr{
		Setpgid: true,
	}
	// only set Credential if caller asked for a change
	curUID, curGID := os.Geteuid(), os.Getegid()
	useCred := false
	credUID, credGID := uint32(curUID), uint32(curGID) // nolint:gosec // this is fine here, go seems to return int for actual uint32
	if uid != 0 {
		useCred, credUID = true, uint32(uid)
	}
	if gid != 0 {
		useCred, credGID = true, uint32(gid)
	}
	if useCred {
		sys.Credential = &syscall.Credential{Uid: credUID, Gid: credGID}
	}
	cmd.SysProcAttr = sys

	if err := cmd.Start(); err != nil {
		return "", "", -1, fmt.Errorf("start failed: %w", err)
	}

	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()

	select {
	case <-done:
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}

	exit := -1
	if ps := cmd.ProcessState; ps != nil {
		if ws, ok := ps.Sys().(syscall.WaitStatus); ok {
			exit = ws.ExitStatus()
		}
	}

	if ctx.Err() == context.DeadlineExceeded {
		return outBuf.String(), errBuf.String(), exit, fmt.Errorf("command timeout after %s", timeout)
	}
	if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() != 0 {
		return outBuf.String(), errBuf.String(), exit, fmt.Errorf("command exited with status %d", exit)
	}

	return outBuf.String(), errBuf.String(), exit, nil
}
