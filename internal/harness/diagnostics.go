package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/redact"
)

type diagnosticBuffer struct{ buffer bytes.Buffer }

func (b *diagnosticBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := (64 << 10) - b.buffer.Len(); remaining > 0 {
		b.buffer.Write(p[:min(len(p), remaining)])
	}
	return n, nil
}

func (b *diagnosticBuffer) String() string { return b.buffer.String() }

func runInstaller(cmd *exec.Cmd) error {
	prepareProcess(cmd)
	var output diagnosticBuffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		return commandDiagnostic(err, output.String())
	}
	return nil
}

func inspectionCommand(ctx context.Context, driver, binary string, args ...string) (*exec.Cmd, error) {
	cmd, err := codex.Command(ctx, binary, args...)
	if err != nil {
		return nil, err
	}
	cmd.Dir, _ = os.UserHomeDir()
	if driver == "claude" {
		env := cmd.Environ()
		cmd.Env = nil
		for _, entry := range env {
			if !strings.HasPrefix(entry, "DISABLE_AUTOUPDATER=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "DISABLE_AUTOUPDATER=1")
	}
	return cmd, nil
}

func commandOutput(cmd *exec.Cmd) ([]byte, error) {
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		err = commandDiagnostic(err, string(exit.Stderr))
	}
	return out, err
}

func commandDiagnostic(err error, output string) error {
	// CLI errors can include proxy credentials and environment assignments.
	detail := strings.TrimSpace(redact.Text(output))
	if len(detail) > 2048 {
		detail = detail[:2048] + "…"
	}
	if detail == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, detail)
}
