package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/ji233-Sun/mujicagent/internal/codex"
)

// Add a manager here, not to controller/UI whitelists. A package manager must
// prove ownership before its update command can be offered.
type managerAdapter struct {
	name              string
	rootArgs, binArgs []string
	update            func(Installation, string) (string, []string, error)
}

var managerAdapters = map[string]managerAdapter{
	"vp": {name: "VP", update: func(i Installation, v string) (string, []string, error) {
		return i.Prefix, []string{"update", "--global", "@openai/codex@" + v}, nil
	}},
	"npm": {name: "npm", update: func(i Installation, v string) (string, []string, error) {
		args := []string{"install", "--global", "--prefix", i.Prefix, "@openai/codex@" + v, "--ignore-scripts", "--no-audit", "--no-fund"}
		if runtime.GOOS == "windows" {
			node, cli, err := windowsNpmRuntime(i.Prefix)
			return node, append([]string{cli}, args...), err
		}
		return filepath.Join(i.Prefix, "bin", "npm"), args, nil
	}},
	"brew": {name: "Homebrew", update: func(i Installation, v string) (string, []string, error) {
		return filepath.Join(i.Prefix, "bin", "brew"), []string{"upgrade", "--cask", "codex"}, nil
	}},
	"bun": {name: "Bun", rootArgs: []string{"pm", "--global", "ls"}, binArgs: []string{"pm", "bin", "--global"},
		update: globalPackageUpdate},
	"pnpm": {name: "pnpm", rootArgs: []string{"root", "--global"}, binArgs: []string{"bin", "--global"},
		update: globalPackageUpdate},
}

func ManagerName(method string) string {
	if a, ok := managerAdapters[method]; ok {
		return a.name
	}
	if method == "custom" || method == "ambiguous" {
		return ""
	}
	return method
}

func (i Installation) CanUpdate() bool {
	if i.UpdateIssue != "" {
		return false
	}
	_, ok := managerAdapters[i.Method]
	return i.Method == "managed" || ok
}

func globalPackageUpdate(i Installation, version string) (string, []string, error) {
	if i.Manager == "" || i.PackageRoot == "" {
		return "", nil, fmt.Errorf("package manager ownership is not verified")
	}
	return i.Manager, []string{"add", "--global", "@openai/codex@" + version, "--ignore-scripts"}, nil
}

// Resolve known Node launchers without executing .cmd through a shell.
func managerCommand(ctx context.Context, binary string, args ...string) (*exec.Cmd, error) {
	if runtime.GOOS == "windows" {
		if wrapper, err := exec.LookPath(binary); err == nil && strings.EqualFold(filepath.Ext(wrapper), ".cmd") && strings.EqualFold(filepath.Base(wrapper), "pnpm.cmd") {
			script := filepath.Join(filepath.Dir(wrapper), "node_modules", "pnpm", "bin", "pnpm.cjs")
			if _, err := os.Stat(script); err != nil {
				return nil, err
			}
			return codex.Command(ctx, "node.exe", append([]string{script}, args...)...)
		}
	}
	return codex.Command(ctx, binary, args...)
}

func canonical(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}

func packageOwner(path string) string {
	owners := map[string]bool{}
	// Only installation roots above node_modules carry ownership metadata.
	// The Codex package's own packageManager field describes its development.
	for dir := filepath.Dir(path); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if filepath.Base(dir) != "node_modules" {
			continue
		}
		root := filepath.Dir(dir)
		var manifest struct {
			PackageManager string `json:"packageManager"`
		}
		if data, err := readSmallFile(filepath.Join(root, "package.json")); err == nil && json.Unmarshal(data, &manifest) == nil {
			id, _, _ := strings.Cut(manifest.PackageManager, "@")
			if id != "" && len(id) < 64 && strings.IndexFunc(id, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') }) < 0 {
				owners[id] = true
			}
		}
		for file, owner := range map[string]string{"bun.lock": "bun", "bun.lockb": "bun", "pnpm-lock.yaml": "pnpm", "yarn.lock": "yarn", "node_modules/.modules.yaml": "pnpm"} {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(file))); err == nil {
				owners[owner] = true
			}
		}
	}
	if len(owners) > 1 {
		return "ambiguous"
	}
	for owner := range owners {
		return owner
	}
	return ""
}

func readSmallFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 64<<10))
}

func probeManager(ctx context.Context, binary string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd, err := managerCommand(ctx, binary, args...)
	if err != nil {
		return "", err
	}
	// Global configuration must not come from the currently open project.
	cmd.Dir, _ = os.UserHomeDir()
	prepareProcess(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func managerRoot(id, output string) string {
	if id == "bun" {
		// bun pm --global ls begins with "<root> node_modules (<count>)".
		line, _, _ := strings.Cut(output, "\n")
		root, _, found := strings.Cut(line, " node_modules")
		if !found {
			return ""
		}
		return filepath.Join(root, "node_modules")
	}
	return output
}

func ownsLauncher(launcher, real, root, bin string) bool {
	pkg := filepath.Join(root, "@openai", "codex")
	var manifest struct{ Name string }
	data, err := readSmallFile(filepath.Join(pkg, "package.json"))
	if err != nil || json.Unmarshal(data, &manifest) != nil || manifest.Name != "@openai/codex" {
		return false
	}
	entry := canonical(filepath.Join(pkg, "bin", "codex.js"))
	if entry == real {
		return true
	}
	if runtime.GOOS == "windows" {
		target, exe, _ := Target(runtime.GOOS, runtime.GOARCH)
		arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
		for _, dir := range []string{pkg, filepath.Join(pkg, "node_modules", "@openai", "codex-win32-"+arch), filepath.Join(root, "@openai", "codex-win32-"+arch)} {
			if canonical(filepath.Join(dir, "vendor", target, "bin", exe)) == real {
				return true
			}
		}
	}
	// pnpm's shim can be a regular script rather than a symlink. Match both
	// its global bin directory and the referenced package, without evaluating it.
	if canonical(filepath.Dir(launcher)) != canonical(bin) {
		return false
	}
	body, err := readSmallFile(launcher)
	if err != nil {
		return false
	}
	script := strings.ReplaceAll(string(body), "\\", "/")
	base := filepath.ToSlash(filepath.Dir(launcher))
	for _, variable := range []string{"${basedir}", "$basedir", "%~dp0", "%dp0%"} {
		script = strings.ReplaceAll(script, variable, base+"/")
	}
	for _, match := range shimPackagePath.FindAllStringSubmatch(script, -1) {
		if filepath.IsAbs(match[1]) && canonical(filepath.FromSlash(match[1])) == entry {
			return true
		}
	}
	return false
}

var shimPackagePath = regexp.MustCompile(`["']([^"'\r\n]*[/]@openai/codex/bin/codex[.]js)["']`)

func inspectPackageManager(ctx context.Context, current Installation, real string) Installation {
	hint := packageOwner(real)
	if hint != "" {
		a, supported := managerAdapters[hint]
		if !supported {
			current.Method, current.UpdateIssue = hint, "unsupported"
			if hint == "ambiguous" {
				current.UpdateIssue = "ambiguous"
			}
			return current
		}
		if len(a.rootArgs) == 0 {
			if current.Method != hint {
				current.Method, current.UpdateIssue = hint, "unverified"
			}
			return current
		}
	}
	// Avoid invoking unrelated tools for arbitrary external executables.
	body, _ := readSmallFile(current.Path)
	if hint == "" && !strings.Contains(filepath.ToSlash(real), "/node_modules/") && !strings.Contains(string(body), "@openai/codex") {
		return current
	}
	var matches []Installation
	var candidates []string
	for id, a := range managerAdapters {
		if len(a.rootArgs) > 0 {
			candidates = append(candidates, id)
		}
	}
	slices.Sort(candidates)
	for _, id := range candidates {
		if hint != "" && hint != id {
			continue
		}
		a := managerAdapters[id]
		binary := id
		if sibling := filepath.Join(filepath.Dir(current.Path), id); runtime.GOOS != "windows" {
			if _, err := os.Stat(sibling); err == nil {
				binary = sibling
			}
		}
		cmd, err := managerCommand(ctx, binary)
		if err != nil {
			if hint == id {
				current.Method, current.UpdateIssue = id, "unavailable"
			}
			continue
		}
		// Keep the original name for known Windows .cmd resolution.
		resolved := cmd.Path
		if runtime.GOOS == "windows" {
			if path, e := exec.LookPath(binary); e == nil {
				resolved = path
			}
		}
		rootOutput, rootErr := probeManager(ctx, resolved, a.rootArgs)
		bin, binErr := probeManager(ctx, resolved, a.binArgs)
		root := managerRoot(id, rootOutput)
		if rootErr != nil || binErr != nil || !filepath.IsAbs(root) || !filepath.IsAbs(bin) || !ownsLauncher(current.Path, real, root, bin) {
			if hint == id {
				current.Method, current.UpdateIssue = id, "unverified"
			}
			continue
		}
		match := current
		match.Method, match.Manager, match.PackageRoot, match.BinDir, match.UpdateIssue = id, resolved, canonical(root), canonical(bin), ""
		matches = append(matches, match)
	}
	if len(matches) == 1 {
		return matches[0]
	}
	if len(matches) > 1 {
		current.Method, current.UpdateIssue = "ambiguous", "ambiguous"
	}
	return current
}
