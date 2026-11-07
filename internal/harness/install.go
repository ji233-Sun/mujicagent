package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ji233-Sun/mujicagent/internal/codex"
)

// InstallPlan is prepared without writes. The preview and execution share the
// same resolved executable, arguments, release and destination.
type InstallPlan struct {
	Current             Installation
	Release             Release
	Source, Stage, Path string
	Command             string
	Args                []string
}

func validSource(source string) bool {
	return source == "" || source == "cn" || source == "native-mirror"
}

func PrepareInstall(ctx context.Context, root, source string, release Release, current Installation) (*InstallPlan, error) {
	if !versionPattern.MatchString(release.Version) {
		return nil, fmt.Errorf("invalid release version")
	}
	if current.Path != "" && current.Method != "managed" && current.Method != "npm" && current.Method != "brew" && current.Method != "vp" {
		return nil, fmt.Errorf("installation manager not recognized")
	}
	if current.Method == "managed" {
		source = current.Source
	}
	if source == "official" {
		source = ""
	}
	if !validSource(source) {
		return nil, fmt.Errorf("invalid installation source")
	}
	p := &InstallPlan{Current: current, Release: release, Source: source, Path: current.Path}
	switch current.Method {
	case "vp":
		p.Command = current.Prefix
		p.Args = []string{"update", "--global", "@openai/codex@" + release.Version}
	case "npm":
		p.Command = filepath.Join(current.Prefix, "bin", "npm")
		p.Args = []string{"install", "--global", "--prefix", current.Prefix, "@openai/codex@" + release.Version, "--ignore-scripts", "--no-audit", "--no-fund"}
		if runtime.GOOS == "windows" {
			node, cli, err := windowsNpmRuntime(current.Prefix)
			if err != nil {
				return nil, err
			}
			p.Command = node
			p.Args = append([]string{cli}, p.Args...)
		}
	case "brew":
		p.Command = filepath.Join(current.Prefix, "bin", "brew")
		p.Args = []string{"upgrade", "--cask", "codex"}
	default:
		p.Stage = filepath.Join(root, fmt.Sprintf("release-%s-%d", release.Version, time.Now().UnixNano()))
		p.Path = filepath.Join(p.Stage, filepath.FromSlash(release.Entry))
		if source == "cn" && runtime.GOOS != "windows" {
			// Freeze the fallback in the preview, never silently change it at execution.
			if cmd, err := codex.Command(ctx, "npm"); err == nil {
				p.Command = cmd.Path
				p.Args = []string{"install", "--prefix", p.Stage, "@openai/codex@" + release.Version, "--registry", registry(source), "--ignore-scripts", "--no-audit", "--no-fund"}
				p.Path = filepath.Join(p.Stage, "node_modules", ".bin", "codex")
			} else if current.Path != "" {
				return nil, fmt.Errorf("npm runtime unavailable: %w", err)
			}
		}
		if source == "cn" && p.Command == "" {
			p.Source = "native-mirror"
		}
	}
	if p.Command != "" {
		cmd, err := codex.Command(ctx, p.Command, p.Args...)
		if err != nil {
			return nil, err
		}
		p.Command, p.Args = cmd.Path, cmd.Args[1:]
	} else if !strings.HasPrefix(release.URL, "https://") || release.Digest == "" || !filepath.IsLocal(filepath.FromSlash(release.Entry)) {
		return nil, fmt.Errorf("invalid native release or missing checksum")
	}
	return p, nil
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
		if err = runInstaller(cmd); err != nil {
			return "", fmt.Errorf("installation failed: %w", err)
		}
	} else if err := downloadExtract(ctx, p.Release, p.Stage); err != nil {
		return "", err
	}
	path, err := verify(ctx, p.Path, p.Release.Version)
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
