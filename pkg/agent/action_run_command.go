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
	"fmt"
	"strconv"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"

	"github.com/google/shlex"
)

// RunCommandAction executes local commands
type RunCommandAction struct{}

// Name returns the name of the action
func (r *RunCommandAction) Name() string {
	return "RunCommand"
}

func splitArgs(cmdline string) ([]string, error) {
	return shlex.Split(cmdline)
}

/*
func executeIsolatedCommand(command string, args []string, chrootDir string, uid, gid int) (string, error) {
	// Prepare attributes for syscall.ForkExec
	attr := &syscall.ProcAttr{
		Env:   []string{"PATH=/usr/bin:/bin"},
		Files: []uintptr{os.Stdin.Fd(), os.Stdout.Fd(), os.Stderr.Fd()},
	}

	// If chrootDir is specified, set it
	if chrootDir != "" {
		attr.Dir = "/"
		err := syscall.Chroot(chrootDir)
		if err != nil {
			return "", fmt.Errorf("failed to chroot to %s: %w", chrootDir, err)
		}
		cmn.DebugMsg(cmn.DbgLvlDebug2, "Chrooted to: %s", chrootDir)
	}

	// Drop privileges if UID and GID are specified
	if uid != 0 {
		err := syscall.Setuid(uid)
		if err != nil {
			return "", fmt.Errorf("failed to set UID: %w", err)
		}
	}
	if gid != 0 {
		err := syscall.Setgid(gid)
		if err != nil {
			return "", fmt.Errorf("failed to set GID: %w", err)
		}
	}
	if uid != 0 || gid != 0 {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "Privileges dropped to UID: %d, GID: %d", uid, gid)
	}

	fmt.Printf("Executing command: %s %v\n", command, args)

	// Execute the command
	pid, err := syscall.ForkExec(command, args, attr) //nolint:gosec // This is a controlled environment
	if err != nil {
		return "", fmt.Errorf("failed to execute command: %w", err)
	}

	// Wait for the process to finish
	var ws syscall.WaitStatus
	_, err = syscall.Wait4(pid, &ws, 0, nil)
	if err != nil {
		return "", fmt.Errorf("failed to wait for process: %w", err)
	}

	// Check exit status
	if ws.ExitStatus() != 0 {
		return "", fmt.Errorf("command exited with status %d", ws.ExitStatus())
	}

	return "Command executed successfully", nil
}
*/

// Execute runs a command in ch-rooted and/or with dropped privileges
func (r *RunCommandAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	rval := make(map[string]interface{})
	rval[StrResponse] = nil
	rval[StrConfig] = nil

	config, err := getConfig(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	rval[StrConfig] = config

	commandRaw, err := getInput(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	// Check if the command is a direct command or a command map
	if commandRaw[StrRequest] == nil {
		// Check if there is a "command" in params
		if params["command"] == nil {
			rval[StrStatus] = StatusError
			rval[StrMessage] = "missing 'command' parameter"
			return rval, fmt.Errorf("missing 'command' parameter")
		}
		commandRaw[StrRequest] = params["command"]
	}
	// Check if commandRaw has an input that is a string or a map
	cmdStr := ""
	commandMap := make(map[string]interface{})
	_, ok := commandRaw[StrRequest].(string)
	if !ok {
		// Check if it's a map
		_, ok = commandRaw[StrRequest].(map[string]interface{})
		if !ok {
			rval[StrStatus] = StatusError
			rval[StrMessage] = "invalid command format"
			return rval, fmt.Errorf("invalid command format")
		}
		commandMap = commandRaw[StrRequest].(map[string]interface{})
	} else {
		// Must be a direct command
		cmdStr = commandRaw[StrRequest].(string)
		commandMap["command"] = cmdStr
	}
	// Check if cmdStr needs to be resolved
	cmdStr = resolveResponseString(commandMap, cmdStr)

	command := cmdStr
	args := strings.Fields(command)
	if len(args) == 0 {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "empty command"
		return rval, fmt.Errorf("empty command")
	}

	argsList := []string{}
	argsList = append(argsList, "")
	argsList = append(argsList, strings.Join(args[1:], " "))

	// Retrieve chrootDir and privileges from parameters
	chrootDir := ""
	if params["chroot_dir"] != nil {
		tChrootDir, ok := params["chroot_dir"].(string)
		if ok {
			// Check if tChrootDir needs to be resolved
			chrootDir = resolveResponseString(commandMap, tChrootDir)
		}
	}
	// Check if we have UID and GID
	uid := uint32(0)
	gid := uint32(0)
	if params["uid"] != nil {
		tUID, ok := params["uid"].(string)
		if ok {
			// Check if tUID needs to be resolved
			tUID = resolveResponseString(commandMap, tUID)
		}
		// Convert tUID to a uint32, checking for valid range
		uidParsed, err := strconv.ParseUint(tUID, 10, 32)
		if err != nil {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("invalid UID: %v", err)
			return rval, err
		}
		uid = uint32(uidParsed) //nolint:gosec // it's ok here
	}
	if params["gid"] != nil {
		tGID, ok := params["gid"].(string)
		if ok {
			// Check if tGID needs to be resolved
			tGID = resolveResponseString(commandMap, tGID)
		}
		// Convert tGID to a uint32, checking for valid range
		gidParsed, err := strconv.ParseUint(tGID, 10, 32)
		if err != nil {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("invalid GID: %v", err)
			return rval, err
		}
		gid = uint32(gidParsed) //nolint:gosec // it's ok here
	}

	// Log execution
	cmn.DebugMsg(cmn.DbgLvlDebug2, "Executing command: %s %v", args[0], argsList)

	// Execute the command in isolation
	stdout, stderr, exitCode, err := executeIsolatedCommand(args[0], argsList, chrootDir, uid, gid, 180*time.Second)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = fmt.Sprintf("command execution failed: %v", err)
		return rval, err
	}

	output := ""
	if stdout == "" {
		output = stderr
	} else {
		output = stdout
	}

	status := ""
	if exitCode == 0 {
		status = StatusSuccess
	} else {
		status = StatusError
	}

	rval[StrResponse] = output
	rval[StrStatus] = status
	rval[StrMessage] = "command executed successfully"

	return rval, nil
}
