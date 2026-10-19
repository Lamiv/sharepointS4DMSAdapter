package ctl

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
)

// Runner starts external programs. It exists so that tests can check the exact argument
// lists and so that nothing is ever run through a shell: name and args are separate values.
type Runner interface {
	Run(ctx context.Context, name string, args []string, opts RunOpts) (Result, error)
}

// RunOpts configures one program run.
type RunOpts struct {
	Dir   string
	Env   []string  // appended to the inherited environment
	Stdin io.Reader // nil: no input
	// Stdout and Stderr, when set, receive the output as it is produced (used for log
	// following). The captured Result is then empty for that stream.
	Stdout io.Writer
	Stderr io.Writer
}

// Result is the outcome of a run that started successfully.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// ErrNotInstalled is returned when the program does not exist on PATH.
var ErrNotInstalled = errors.New("program not found")

// ExecRunner is the real Runner.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args []string, opts RunOpts) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = opts.Dir
	cmd.Env = append(os.Environ(), opts.Env...)
	cmd.Stdin = opts.Stdin
	var so, se bytes.Buffer
	if opts.Stdout != nil {
		cmd.Stdout = opts.Stdout
	} else {
		cmd.Stdout = &so
	}
	if opts.Stderr != nil {
		cmd.Stderr = opts.Stderr
	} else {
		cmd.Stderr = &se
	}
	err := cmd.Run()
	res := Result{Stdout: so.String(), Stderr: se.String()}
	if err == nil {
		return res, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		return res, nil // started and exited non-zero: the caller inspects the Result
	}
	if errors.Is(err, exec.ErrNotFound) {
		return res, ErrNotInstalled
	}
	return res, err
}
