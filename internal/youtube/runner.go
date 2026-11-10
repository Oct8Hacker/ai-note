package youtube

import (
	"bytes"
	"context"
	"os/exec"
)

// Runner executes a program and keeps its output and diagnostics separate.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}

// ExecRunner starts real subprocesses using the supplied context.
type ExecRunner struct{}

// Run executes the program with individual arguments.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
