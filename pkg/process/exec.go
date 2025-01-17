// Copyright © 2022 FORTH-ICS
// Copyright (c) 2022 Kubeshop
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package process

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

var GoEnviron []string // "MY_VAR=some_value"

// Execute runs system command and returns whole output also in case of error
func Execute(command string, arguments ...string) (out []byte, err error) {
	return ExecuteInDir("", command, arguments...)
}

// ExecuteInDir runs system command and returns whole output also in case of error in a specific directory
func ExecuteInDir(dir string, command string, arguments ...string) (out []byte, err error) {
	cmd := exec.Command(command, arguments...)
	if dir != "" {
		cmd.Dir = dir
	}

	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, GoEnviron...)

	buffer := new(bytes.Buffer)
	cmd.Stdout = buffer
	cmd.Stderr = buffer

	if err = cmd.Start(); err != nil {
		return buffer.Bytes(), fmt.Errorf("could not start process: %w", err)
	}

	if err = cmd.Wait(); err != nil {
		// TODO clean error output (currently it has buffer too - need to refactor in cmd)
		return buffer.Bytes(), fmt.Errorf("process error: %w\noutput: %s", err, buffer.String())
	}

	return buffer.Bytes(), nil
}

// LoggedExecuteInDir runs system command and returns whole output also in case of error in a specific directory with logging to writer
func LoggedExecuteInDir(dir string, writer io.Writer, command string, arguments ...string) (out []byte, err error) {
	cmd := exec.Command(command, arguments...)
	if dir != "" {
		cmd.Dir = dir
	}
	buffer := new(bytes.Buffer)

	// set multiwriter write to writer and to buffer in parallel
	w := io.MultiWriter(buffer, writer)
	cmd.Stdout = w
	cmd.Stderr = w

	if err = cmd.Start(); err != nil {
		return buffer.Bytes(), fmt.Errorf("could not start process: %w", err)
	}

	if err = cmd.Wait(); err != nil {
		return buffer.Bytes(), fmt.Errorf("process error: %w", err)
	}

	return buffer.Bytes(), nil
}

// ExecuteAsync runs system command and doesn't wait when it's completed.
func ExecuteAsync(command string, arguments ...string) (cmd *exec.Cmd, err error) {
	return ExecuteAsyncInDir("", command, arguments...)
}

// ExecuteAsyncInDir runs system command and doesn't wait when it's completed for specific directory.
func ExecuteAsyncInDir(dir string, command string, arguments ...string) (cmd *exec.Cmd, err error) {
	cmd = exec.Command(command, arguments...)
	if dir != "" {
		cmd.Dir = dir
	}

	if err = cmd.Start(); err != nil {
		return cmd, fmt.Errorf("process error: %w", err)
	}

	return cmd, nil
}

// ExecuteString executes string based command.
func ExecuteString(command string) (out []byte, err error) {
	parts := strings.Split(command, " ")
	if len(parts) == 1 {
		out, err = Execute(parts[0])
	} else if len(parts) > 1 {
		out, err = Execute(parts[0], parts[1:]...)
	} else {
		return out, fmt.Errorf("invalid command to run '%s'", command)
	}

	if err != nil {
		return out, fmt.Errorf("error: %w, output: %s", err, out)
	}

	return out, nil
}
