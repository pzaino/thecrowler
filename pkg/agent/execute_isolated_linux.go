//go:build linux
// +build linux

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
	"reflect"
	"syscall"
	"time"
)

// trySetNoNewPrivs sets SysProcAttr.NoNewPrivs= true if the field exists.
func trySetNoNewPrivs(sys *syscall.SysProcAttr) {
	if sys == nil {
		return
	}
	v := reflect.ValueOf(sys).Elem()
	f := v.FieldByName("NoNewPrivs")
	if f.IsValid() && f.CanSet() && f.Kind() == reflect.Bool {
		f.SetBool(true)
	}
}

// executeIsolatedCommand (Linux)
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

	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, command, args...)

	// capture I/O
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	if chrootDir != "" {
		cmd.Dir = "/" // path inside chroot
	}

	// build SysProcAttr
	sys := &syscall.SysProcAttr{
		Setpgid:   true,            // allow killing the whole process group
		Pdeathsig: syscall.SIGKILL, // kill child if parent dies
	}
	// child-only chroot (requires privilege)
	if chrootDir != "" {
		sys.Chroot = chrootDir
	}
	// only set Credential if caller asked for a change
	curUID, curGID := os.Geteuid(), os.Getegid()
	useCred := false
	credUID, credGID := uint32(curUID), uint32(curGID)
	if uid != 0 {
		useCred, credUID = true, uint32(uid)
	}
	if gid != 0 {
		useCred, credGID = true, uint32(gid)
	}
	if useCred {
		sys.Credential = &syscall.Credential{Uid: credUID, Gid: credGID}
	}
	// opportunistically enable NoNewPrivs when available
	trySetNoNewPrivs(sys)

	cmd.SysProcAttr = sys

	// start & wait with timeout handling
	if err := cmd.Start(); err != nil {
		return "", "", -1, fmt.Errorf("start failed: %w", err)
	}

	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()

	select {
	case <-done:
	case <-ctx.Done():
		// timeout: kill entire process group (-pid)
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}

	// exit code
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
