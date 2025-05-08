package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"

	"github.com/pkg/errors"
)

// capture executes the command specified by args and returns its stdout. If
// the process exits with a failing exit code, capture instead returns an error
// which includes the process's stderr.
func capture(args ...string) (string, error) {
	var cmd *exec.Cmd
	if len(args) == 0 {
		panic("capture called with no arguments")
	} else if len(args) == 1 {
		cmd = exec.Command(args[0])
	} else {
		cmd = exec.Command(args[0], args[1:]...)
	}
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			err = errors.Errorf("%s: %s", err, exitErr.Stderr)
		}
		return "", err
	}
	return string(bytes.TrimSpace(out)), err
}

// spawn executes the command specified by args. The subprocess inherits the
// current processes's stdin, stdout, and stderr streams. If the process exits
// with a failing exit code, run returns a generic "process exited with
// status..." error, as the process has likely written an error message to
// stderr.
func spawn(args ...string) error {
	return spawnWith(os.Stdin, os.Stdout, os.Stderr, args...)
}

// spawnWith executes the command specified by args using the provided reader
// and writers for process I/O. The subprocess inherits the current processes's
// stdin, stdout, and stderr streams. If the process exits with a failing exit
// code, run returns a generic "process exited with status..." error, as the
// process has likely written an error message to stderr.
func spawnWith(in io.Reader, out, err io.Writer, args ...string) error {
	var cmd *exec.Cmd
	if len(args) == 0 {
		panic("spawn called with no arguments")
	} else if len(args) == 1 {
		cmd = exec.Command(args[0])
	} else {
		cmd = exec.Command(args[0], args[1:]...)
	}

	// Ensure that GODEBUG=[...,]runtimecontentionstacks=1 is set to improve
	// mutex profiles.
	env := os.Environ()
	envGodebug := os.Getenv("GODEBUG")
	if envGodebug != "" {
		envGodebug += ","
	}
	envGodebug += "runtimecontentionstacks=1"
	cmd.Env = append(env, "GODEBUG="+envGodebug)

	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = err
	return cmd.Run()
}
