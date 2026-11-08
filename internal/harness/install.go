package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/ji233-Sun/mujicagent/internal/codex"
)

// InstallPlan is prepared without writes. The preview and execution share the
// same resolved executable, arguments, release and destination.
type InstallPlan struct {
	Driver              string
	Current             Installation
	Release             Release
	Source, Stage, Path string
	Command             string
	Args                []string
	Dir                 string
}

func validSource(driver, source string) bool {
	return slices.Contains(installerAdapters[driver].sources, source)
}

func PrepareInstall(ctx context.Context, driver, root, source string, release Release, current Installation) (*InstallPlan, error) {
	if _, ok := installerAdapters[driver]; !ok {
		return nil, fmt.Errorf("unsupported harness: %s", driver)
	}
	if current.Driver != "" && current.Driver != driver {
		return nil, fmt.Errorf("installation belongs to another harness")
	}
	if current.Path != "" {
		current.Driver = driver
	}
	if !versionPattern.MatchString(release.Version) {
		return nil, fmt.Errorf("invalid release version")
	}
	if current.Path != "" && !current.CanUpdate() {
		return nil, fmt.Errorf("automatic update unavailable for %s; use the original package manager", current.Method)
	}
	if current.Method == "managed" {
		source = current.Source
	}
	if source == "official" {
		source = ""
	}
	if !validSource(driver, source) {
		return nil, fmt.Errorf("invalid installation source")
	}
	p := &InstallPlan{Driver: driver, Current: current, Release: release, Source: source, Path: current.Path}
	if adapter, ok := managerAdapters[current.Method]; ok {
		var err error
		p.Command, p.Args, err = adapter.update(current, release.Version)
		if err != nil {
			return nil, err
		}
		if current.Manager != "" || current.Method == "native" {
			p.Dir, _ = os.UserHomeDir()
		}
	} else {
		p.Stage = filepath.Join(root, fmt.Sprintf("release-%s-%d", release.Version, time.Now().UnixNano()))
		p.Path = filepath.Join(p.Stage, filepath.FromSlash(release.Entry))
		if source == "npm" || source == "cn" {
			cmd, err := managerCommand(ctx, "npm")
			if err == nil && !(driver == "codex" && runtime.GOOS == "windows") {
				p.Command, p.Args = cmd.Path, append(cmd.Args[1:], "install", "--prefix", p.Stage, packageFor(driver)+"@"+release.Version, "--registry", registry(source), "--ignore-scripts", "--no-audit", "--no-fund")
				p.Args = installerScriptArgs(driver, p.Args)
				p.Path = filepath.Join(p.Stage, "node_modules", ".bin", driver)
				if runtime.GOOS == "windows" {
					p.Path += ".cmd"
				}
			} else if current.Path != "" || source == "npm" || driver != "codex" {
				return nil, fmt.Errorf("npm runtime unavailable; install Node.js and npm, or select the official native package: %v", err)
			} else {
				p.Source = "native-mirror"
			}
		}
	}
	if p.Command != "" {
		if driver != "codex" && current.Method != "native" && current.Method != "brew" {
			if err := checkNodeRuntime(ctx, driver); err != nil {
				return nil, err
			}
		}
		cmd, err := managerCommand(ctx, p.Command, p.Args...)
		if err != nil {
			return nil, err
		}
		p.Command, p.Args = cmd.Path, cmd.Args[1:]
	} else if !strings.HasPrefix(release.URL, "https://") || release.Digest == "" || !filepath.IsLocal(filepath.FromSlash(release.Entry)) {
		return nil, fmt.Errorf("invalid native release or missing checksum")
	}
	return p, nil
}

func checkNodeRuntime(ctx context.Context, driver string) error {
	minimum := "20.0.0"
	if driver == "pi" {
		minimum = "22.19.0"
	}
	if driver == "claude" {
		minimum = "22.0.0"
	}
	cmd, err := codex.Command(ctx, "node", "--version")
	if err == nil {
		prepareProcess(cmd)
		var out []byte
		out, err = cmd.Output()
		version := strings.TrimPrefix(strings.TrimSpace(string(out)), "v")
		if err == nil && versionPattern.MatchString(version) && !Newer(minimum, version) {
			return nil
		}
	}
	return fmt.Errorf("%s npm installation requires Node.js %s or newer; select the official native package for installation without Node.js", driver, minimum)
}

func windowsNpmRuntime(prefix string) (string, string, error) {
	root := prefix
	cli := filepath.Join(root, "node_modules", "npm", "bin", "npm-cli.js")
	if _, err := os.Stat(cli); err != nil {
		// Windows commonly keeps global packages in AppData but npm itself
		// beside node.exe in Program Files. Preserve the package prefix.
		wrapper, err := exec.LookPath("npm.cmd")
		if err != nil {
			return "", "", err
		}
		root = filepath.Dir(wrapper)
		cli = filepath.Join(root, "node_modules", "npm", "bin", "npm-cli.js")
		if _, err := os.Stat(cli); err != nil {
			return "", "", fmt.Errorf("npm runtime unavailable: %w", err)
		}
	}
	node := filepath.Join(root, "node.exe")
	if _, err := os.Stat(node); err != nil {
		node = "node.exe"
	}
	return node, cli, nil
}

// CommandPreview is display-only. Execution always uses an argument array.
func (p *InstallPlan) CommandPreview() string {
	if p.Command == "" {
		return ""
	}
	args := append([]string{p.Command}, p.Args...)
	for i, arg := range args {
		if strings.ContainsAny(arg, " \t\r\n'\"`$;&|<>(){}[]!*?\\") {
			if runtime.GOOS == "windows" {
				args[i] = "'" + strings.ReplaceAll(arg, "'", "''") + "'"
			} else {
				args[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
			}
		}
	}
	prefix := ""
	if runtime.GOOS == "windows" {
		prefix = "& "
	}
	return prefix + strings.Join(args, " ")
}

func (p *InstallPlan) Execute(ctx context.Context) (string, error) {
	success := false
	if p.Stage != "" {
		if err := os.MkdirAll(filepath.Dir(p.Stage), 0700); err != nil {
			return "", err
		}
		if err := os.Mkdir(p.Stage, 0700); err != nil {
			return "", err
		}
		defer func() {
			if !success {
				os.RemoveAll(p.Stage)
			}
		}()
	}
	if p.Command != "" {
		cmd, err := codex.Command(ctx, p.Command, p.Args...)
		if err != nil {
			return "", err
		}
		cmd.Dir = p.Dir
		if err = runInstaller(cmd); err != nil {
			return "", fmt.Errorf("installation failed: %w", err)
		}
	} else if err := downloadExtract(ctx, p.Release, p.Stage); err != nil {
		return "", err
	}
	path, err := verify(ctx, p.Driver, p.Path, p.Release.Version)
	if err != nil {
		return "", err
	}
	if p.Stage != "" {
		data, _ := json.Marshal(p.Source)
		if err := os.WriteFile(filepath.Join(p.Stage, "source.json"), data, 0600); err != nil {
			return "", err
		}
	}
	success = true
	return path, nil
}
