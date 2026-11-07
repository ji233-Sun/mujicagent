package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/ji233-Sun/mujicagent/internal/codex"
)

// rpcProcess owns pipes, cancellation and reaping for line-oriented CLI RPC.
// Protocol parsing and settlement remain the responsibility of each driver.
type rpcProcess struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	input  *json.Encoder
	output *bufio.Scanner
	cancel context.CancelFunc
}

func startRPCProcess(ctx context.Context, binary, cwd string, args []string) (*rpcProcess, error) {
	return startRPCProcessEnv(ctx, binary, cwd, args, nil)
}

func startRPCProcessEnv(ctx context.Context, binary, cwd string, args []string, env map[string]string) (*rpcProcess, error) {
	ctx, cancel := context.WithCancel(ctx)
	cmd, err := codex.Command(ctx, binary, args...)
	if err != nil {
		cancel()
		return nil, err
	}
	if len(env) != 0 {
		base := cmd.Env
		if base == nil {
			base = os.Environ()
		}
		cmd.Env = nil
		for _, entry := range base {
			key, _, _ := strings.Cut(entry, "=")
			if _, replace := env[key]; !replace {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		for key, value := range env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Dir, cmd.Stderr = cwd, io.Discard
	in, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		cancel()
		return nil, err
	}
	prepareProcess(cmd)
	if err := cmd.Start(); err != nil {
		in.Close()
		out.Close()
		cancel()
		return nil, err
	}
	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	return &rpcProcess{cmd: cmd, in: in, input: json.NewEncoder(in), output: scanner, cancel: cancel}, nil
}

func (p *rpcProcess) close() {
	p.cancel()
	p.in.Close()
	_ = p.cmd.Wait()
}

func (c *Control) attach(p *rpcProcess, driver string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.in, c.driver = p.input, driver
}
