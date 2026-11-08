package codex

import (
	"context"
	"encoding/json"
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
		if runtime.GOOS == "windows" {
			resolved, err := exec.LookPath(binary)
			if err != nil {
				return nil, err
			}
			if strings.EqualFold(filepath.Ext(resolved), ".cmd") || strings.EqualFold(filepath.Ext(resolved), ".bat") {
				if strings.EqualFold(strings.TrimSuffix(filepath.Base(resolved), filepath.Ext(resolved)), "claude") {
					entry, script, err := windowsClaudeEntry(resolved)
					if err != nil {
						return nil, err
					}
					if script {
						node, err := exec.LookPath("node.exe")
						if err != nil {
							return nil, err
						}
						cmd := exec.Command(node, append([]string{entry}, args...)...)
						return cmd, cmd.Err
					}
					cmd := exec.Command(entry, args...)
					return cmd, cmd.Err
				}
				if strings.EqualFold(strings.TrimSuffix(filepath.Base(resolved), filepath.Ext(resolved)), "droid") {
					native, err := windowsDroidNative(resolved, runtime.GOARCH)
					if err != nil {
						return nil, err
					}
					cmd := exec.Command(native, args...)
					return cmd, cmd.Err
				}
				if strings.EqualFold(strings.TrimSuffix(filepath.Base(resolved), filepath.Ext(resolved)), "pi") {
					script, err := windowsPiScript(resolved)
					if err != nil {
						return nil, err
					}
					node, err := exec.LookPath("node.exe")
					if err != nil {
						return nil, err
					}
					cmd := exec.Command(node, append([]string{script}, args...)...)
					return cmd, cmd.Err
				}
				native, err := windowsCodexNative(resolved, runtime.GOARCH)
				if err != nil {
					return nil, err
				}
				binary = native
			} else {
				binary = resolved
			}
		}
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

func windowsClaudeEntry(wrapper string) (string, bool, error) {
	for _, root := range windowsNpmRoots(wrapper) {
		pkg := filepath.Join(root, "@anthropic-ai", "claude-code")
		// New packages use an official native postinstall; older packages use
		// cli.js. Only these package-owned entries are allowed, never cmd.exe.
		for _, entry := range []string{"bin/claude.exe", "cli.js"} {
			path := filepath.Join(pkg, filepath.FromSlash(entry))
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path, entry == "cli.js", nil
			}
		}
	}
	return "", false, &exec.Error{Name: wrapper, Err: exec.ErrNotFound}
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

// Command shares desktop PATH resolution with installation and version probes.
func Command(ctx context.Context, binary string, args ...string) (*exec.Cmd, error) {
	cmd, err := desktopCommand(ctx, binary, args...)
	if err != nil {
		return nil, err
	}
	run := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	run.Env = cmd.Env
	run.WaitDelay = time.Second
	return run, nil
}

// Resolve the known npm layout without cmd.exe interpolation. Unknown wrappers require an explicit exe.
func windowsCodexNative(wrapper, arch string) (string, error) {
	if !strings.EqualFold(strings.TrimSuffix(filepath.Base(wrapper), filepath.Ext(wrapper)), "codex") {
		return "", &exec.Error{Name: wrapper, Err: exec.ErrNotFound}
	}
	target := map[string]string{"amd64": "x86_64-pc-windows-msvc", "arm64": "aarch64-pc-windows-msvc"}[arch]
	suffix := map[string]string{"amd64": "x64", "arm64": "arm64"}[arch]
	if target == "" {
		return "", &exec.Error{Name: wrapper, Err: exec.ErrNotFound}
	}
	root := filepath.Join(filepath.Dir(wrapper), "node_modules", "@openai")
	candidates := []string{
		filepath.Join(root, "codex", "node_modules", "@openai", "codex-win32-"+suffix, "vendor", target, "bin", "codex.exe"),
		filepath.Join(root, "codex-win32-"+suffix, "vendor", target, "bin", "codex.exe"),
		filepath.Join(root, "codex", "vendor", target, "bin", "codex.exe"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", &exec.Error{Name: wrapper, Err: exec.ErrNotFound}
}

// Resolve only known npm package layouts; never execute arbitrary shell wrappers.
func windowsPiScript(wrapper string) (string, error) {
	for _, root := range windowsNpmRoots(wrapper) {
		for _, scope := range []string{"@earendil-works", "@mariozechner"} {
			for _, entry := range []string{"dist/bundle/cli.js", "dist/cli.js"} {
				path := filepath.Join(root, scope, "pi-coding-agent", filepath.FromSlash(entry))
				if info, err := os.Stat(path); err == nil && !info.IsDir() {
					return path, nil
				}
			}
		}
	}
	root := filepath.Join(filepath.Dir(filepath.Dir(wrapper)), "install")
	data, err := os.ReadFile(filepath.Join(root, "managed-install.json"))
	var marker struct {
		Kind, Layout  string
		SchemaVersion int
	}
	if err == nil && json.Unmarshal(data, &marker) == nil && marker.Kind == "pi-managed-install" && marker.Layout == "releases-v1" && marker.SchemaVersion == 1 {
		data, err := os.ReadFile(filepath.Join(root, "current-version"))
		version := strings.TrimSpace(string(data))
		if err == nil && version != "" && version != "." && version != ".." && !strings.ContainsAny(version, `/\:`) {
			for _, entry := range []string{"dist/bundle/cli.js", "dist/cli.js"} {
				path := filepath.Join(root, "releases", version, "node_modules", "@earendil-works", "pi-coding-agent", filepath.FromSlash(entry))
				if info, err := os.Stat(path); err == nil && !info.IsDir() {
					return path, nil
				}
			}
		}
	}
	return "", &exec.Error{Name: wrapper, Err: exec.ErrNotFound}
}

func windowsNpmRoots(wrapper string) []string {
	dir := filepath.Dir(wrapper)
	if filepath.Base(dir) == ".bin" {
		return []string{filepath.Dir(dir)}
	}
	return []string{filepath.Join(dir, "node_modules")}
}

func windowsDroidNative(wrapper, arch string) (string, error) {
	cpu := map[string]string{"amd64": "x64-baseline", "arm64": "arm64"}[arch]
	if cpu != "" {
		for _, root := range windowsNpmRoots(wrapper) {
			for _, base := range []string{root, filepath.Join(root, "@factory", "cli", "node_modules")} {
				path := filepath.Join(base, "@factory", "cli-win32-"+cpu, "bin", "droid.exe")
				if info, err := os.Stat(path); err == nil && !info.IsDir() {
					return path, nil
				}
			}
		}
	}
	return "", &exec.Error{Name: wrapper, Err: exec.ErrNotFound}
}
