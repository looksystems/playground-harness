// Package openshell provides a shell.Driver that executes commands in an
// OpenShell sandbox via SSH or native gRPC.
//
// This file contains the SSH transport — a thin wrapper around
// `ssh user@host -p port '<cmd>'` via os/exec. The interface is extracted
// so tests can inject a fake transport without spinning up a real SSH
// server (see driver_test.go).
package openshell

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// rawResult mirrors the Python driver's internal {"stdout","stderr","exitCode"}
// dictionary — the decoupled shape that both transports agree on before
// the driver wraps it into a shell.ExecResult.
type rawResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// sshTransport is the interface the driver uses to talk to a remote
// sandbox over SSH. Extracted so tests can plug in a scripted fake.
type sshTransport interface {
	// Exec runs command via the SSH connection identified by user, host,
	// port and returns its aggregated output. env is merged into the
	// subprocess environment on top of os.Environ(); it does not flow to
	// the remote side — standard SSH does not forward arbitrary env vars
	// without server-side AcceptEnv configuration. This matches the
	// Python driver's behaviour (subprocess.run env=).
	Exec(ctx context.Context, user, host string, port int, command string, env map[string]string) (rawResult, error)
}

// sshExecTransport is the production SSH transport: it shells out to the
// `ssh` binary via os/exec.
type sshExecTransport struct {
	// Timeout bounds a single SSH invocation. Defaults to 30s to match
	// the Python reference (subprocess.run(timeout=30)).
	Timeout time.Duration
}

// defaultSSHTransport constructs an sshExecTransport with the default
// 30-second per-call timeout.
func defaultSSHTransport() sshTransport {
	return &sshExecTransport{Timeout: 30 * time.Second}
}

// Exec runs `ssh` as a subprocess. The BatchMode/StrictHostKeyChecking
// flags mirror the Python driver so CI environments without a known_hosts
// entry still work.
func (t *sshExecTransport) Exec(
	ctx context.Context,
	user, host string,
	port int,
	command string,
	env map[string]string,
) (rawResult, error) {
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{
		"-p", fmt.Sprintf("%d", port),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		fmt.Sprintf("%s@%s", user, host),
		command,
	}
	cmd := exec.CommandContext(runCtx, "ssh", args...)

	merged := os.Environ()
	for k, v := range env {
		merged = append(merged, k+"="+v)
	}
	cmd.Env = merged

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
			err = nil // non-zero exit is represented in rawResult, not Go error
		} else if runCtx.Err() != nil {
			// Timeout / cancellation.
			return rawResult{
				Stdout:   stdout.String(),
				Stderr:   stderr.String(),
				ExitCode: -1,
			}, fmt.Errorf("ssh transport: %w", runCtx.Err())
		} else {
			return rawResult{}, fmt.Errorf("ssh transport: %w", err)
		}
	}

	return rawResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, err
}
