package codex

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// desktopCommand resolves the CLI and its runtime without changing the app's
// environment. Finder launches do not inherit the user's terminal PATH.
func desktopCommand(ctx context.Context, binary string, args ...string) (*exec.Cmd, error) {
	if runtime.GOOS != "darwin" {
		cmd := exec.Command(binary, args...)
		return cmd, cmd.Err
	}
	home, _ := os.UserHomeDir()
	paths := []string{loginShellPath(ctx, home), os.Getenv("PATH"), "/opt/homebrew/bin:/usr/local/bin"}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if home != "" {
		paths = append(paths, filepath.Join(home, ".local", "bin"), filepath.Join(home, "bin"))
	}
	var dirs []string
	seen := map[string]bool{}
	for _, path := range paths {
		for _, dir := range filepath.SplitList(path) {
			// Never implicitly resolve a CLI from the current project directory.
			if filepath.IsAbs(dir) && !seen[dir] {
				dirs = append(dirs, dir)
				seen[dir] = true
			}
		}
	}
	var path string
	var err error
	if strings.ContainsRune(binary, '/') {
		// An explicit path must not silently select a different installation.
		path, err = exec.LookPath(binary)
	} else {
		err = &exec.Error{Name: binary, Err: exec.ErrNotFound}
		if binary != "" {
			for _, dir := range dirs {
				if candidate, lookupErr := exec.LookPath(filepath.Join(dir, binary)); lookupErr == nil {
					path, err = candidate, nil
					break
				}
			}
		}
	}
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(path, args...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "PATH=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "PATH="+strings.Join(dirs, string(os.PathListSeparator)))
	return cmd, nil
}

func loginShellPath(ctx context.Context, home string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	shell := os.Getenv("SHELL")
	if !filepath.IsAbs(shell) {
		shell = "/bin/zsh"
	}
	// The fixed probe reads only PATH. NUL delimiters exclude profile banners;
	// the configured executable is never interpolated into a shell command.
	cmd := exec.CommandContext(ctx, shell, "-ilc", `printf '\000'; /usr/bin/printenv PATH; printf '\000'`)
	cmd.Dir = home
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	parts := strings.Split(string(out), "\x00")
	if len(parts) < 3 {
		return ""
	}
	return strings.TrimSuffix(parts[1], "\n")
}
