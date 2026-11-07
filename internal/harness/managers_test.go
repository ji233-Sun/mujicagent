package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func managerFixture(t *testing.T, id string, shim bool) (launcher, manager, root, log string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	base := filepath.Join(t.TempDir(), "用户 custom installation")
	bin := filepath.Join(base, "bin")
	root = filepath.Join(base, "global", "node_modules")
	pkg := filepath.Join(root, "@openai", "codex")
	for _, dir := range []string{bin, filepath.Join(pkg, "bin")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(pkg, "package.json"), `{"name":"@openai/codex","packageManager":"pnpm@10"}`)
	data, _ := json.Marshal(map[string]string{"packageManager": id + "@1.0.0"})
	write(filepath.Join(filepath.Dir(root), "package.json"), string(data))
	version := filepath.Join(base, "version")
	write(version, "0.167.0")
	entry := filepath.Join(pkg, "bin", "codex.js")
	write(entry, "#!/bin/sh\nif [ \"$1\" = --version ]; then printf 'codex-cli '; cat \"$HARNESS_MANAGER_VERSION\"; printf '\\n'; fi\n")
	launcher = filepath.Join(bin, "codex")
	if shim {
		write(launcher, "#!/bin/sh\nbasedir=$(dirname \"$0\")\nexec \"$basedir/../global/node_modules/@openai/codex/bin/codex.js\" \"$@\"\n")
	} else if err := os.Symlink(entry, launcher); err != nil {
		t.Fatal(err)
	}
	manager = filepath.Join(bin, id)
	log = filepath.Join(base, "updated")
	t.Setenv("HARNESS_MANAGER_VERSION", version)
	t.Setenv("HARNESS_MANAGER_ROOT", root)
	t.Setenv("HARNESS_MANAGER_BIN", bin)
	t.Setenv("HARNESS_MANAGER_LOG", log)
	write(manager, `#!/bin/sh
case "$1" in
root) printf '%s\n' "$HARNESS_MANAGER_ROOT";;
bin) printf '%s\n' "$HARNESS_MANAGER_BIN";;
pm)
 if [ "$2" = bin ]; then printf '%s\n' "$HARNESS_MANAGER_BIN"
 else printf '%s node_modules (1)\n' "${HARNESS_MANAGER_ROOT%/node_modules}"; fi;;
add)
 printf '%s\n' "$@" > "$HARNESS_MANAGER_LOG"
 printf '0.168.0' > "$HARNESS_MANAGER_VERSION";;
*) exit 1;;
esac
`)
	return
}

func TestManagerAdaptersDiscoverPreviewExecute(t *testing.T) {
	for _, id := range []string{"bun", "pnpm"} {
		for _, shim := range []bool{false, true} {
			t.Run(id+map[bool]string{false: "-symlink", true: "-shim"}[shim], func(t *testing.T) {
				path, manager, root, log := managerFixture(t, id, shim)
				current, err := Inspect(context.Background(), path, t.TempDir())
				if err != nil || current.Method != id || current.Manager != manager || current.PackageRoot != canonical(root) || !current.CanUpdate() {
					t.Fatalf("ownership: %+v %v", current, err)
				}
				p, err := PrepareInstall(context.Background(), t.TempDir(), "", Release{Version: "0.168.0"}, current)
				if err != nil {
					t.Fatal(err)
				}
				want := []string{"add", "--global", "@openai/codex@0.168.0", "--ignore-scripts"}
				if p.Command != manager || !slices.Equal(p.Args, want) || !strings.Contains(p.CommandPreview(), manager) {
					t.Fatal(p)
				}
				if _, err := os.Stat(log); !os.IsNotExist(err) {
					t.Fatal("preview updated packages")
				}
				result, err := p.Execute(context.Background())
				if err != nil || result != path {
					t.Fatal(result, err)
				}
				args, err := os.ReadFile(log)
				if err != nil || string(args) != strings.Join(want, "\n")+"\n" {
					t.Fatal(string(args), err)
				}
			})
		}
	}
}

func TestManagerOwnershipFailures(t *testing.T) {
	for _, mode := range []string{"unsupported", "unverified", "unavailable", "ambiguous"} {
		t.Run(mode, func(t *testing.T) {
			id := "bun"
			if mode == "unsupported" {
				id = "vlt"
			}
			path, manager, root, log := managerFixture(t, id, false)
			switch mode {
			case "unverified":
				t.Setenv("HARNESS_MANAGER_ROOT", t.TempDir())
			case "unavailable":
				if err := os.Chmod(manager, 0600); err != nil {
					t.Fatal(err)
				}
			case "ambiguous":
				if err := os.WriteFile(filepath.Join(filepath.Dir(root), "pnpm-lock.yaml"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			current, err := Inspect(context.Background(), path, t.TempDir())
			if err != nil || current.UpdateIssue != mode || current.CanUpdate() {
				t.Fatal(current, err)
			}
			if _, err := PrepareInstall(context.Background(), t.TempDir(), "", Release{Version: "0.168.0"}, current); err == nil {
				t.Fatal("unsupported ownership produced update plan")
			}
			if _, err := os.Stat(log); !os.IsNotExist(err) {
				t.Fatal("ownership probe updated packages")
			}
		})
	}
}

func TestManagerAdapterExtension(t *testing.T) {
	// A new manager needs one adapter; no controller or view whitelist changes.
	const id = "fixture-pm"
	managerAdapters[id] = managerAdapter{name: "Fixture PM", rootArgs: []string{"root"}, binArgs: []string{"bin"}, update: globalPackageUpdate}
	t.Cleanup(func() { delete(managerAdapters, id) })
	path, manager, _, _ := managerFixture(t, id, false)
	current, err := Inspect(context.Background(), path, t.TempDir())
	if err != nil || !current.CanUpdate() || ManagerName(current.Method) != "Fixture PM" {
		t.Fatal(current, err)
	}
	p, err := PrepareInstall(context.Background(), t.TempDir(), "", Release{Version: "0.168.0"}, current)
	if err != nil || p.Command != manager {
		t.Fatal(p, err)
	}
	if _, err := p.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPnpmIsolatedPackageLayout(t *testing.T) {
	path, _, root, _ := managerFixture(t, "pnpm", true)
	pkg := filepath.Join(root, "@openai", "codex")
	isolated := filepath.Join(root, ".pnpm", "@openai+codex@0.167.0", "node_modules", "@openai", "codex")
	if err := os.MkdirAll(filepath.Dir(isolated), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(pkg, isolated); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(isolated, pkg); err != nil {
		t.Fatal(err)
	}
	current, err := Inspect(context.Background(), path, t.TempDir())
	if err != nil || current.Method != "pnpm" || !current.CanUpdate() {
		t.Fatal(current, err)
	}
	// A different configured global bin invalidates the same wrapper.
	t.Setenv("HARNESS_MANAGER_BIN", t.TempDir())
	changed, err := Inspect(context.Background(), path, t.TempDir())
	if err != nil || changed.CanUpdate() {
		t.Fatal("accepted unrelated global bin", changed, err)
	}
}

func TestWindowsPnpmManagerLauncher(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows executable resolver")
	}
	root := filepath.Join(t.TempDir(), "工具 Program Files")
	cli := filepath.Join(root, "node_modules", "pnpm", "bin", "pnpm.cjs")
	if err := os.MkdirAll(filepath.Dir(cli), 0700); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(root, "pnpm.cmd")
	node := filepath.Join(root, "node.exe")
	for _, path := range []string{cli, wrapper, node} {
		if err := os.WriteFile(path, nil, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", root)
	cmd, err := managerCommand(context.Background(), wrapper, "add", "--global", "@openai/codex@0.168.0")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != node || !slices.Equal(cmd.Args[1:], []string{cli, "add", "--global", "@openai/codex@0.168.0"}) {
		t.Fatal("pnpm wrapper was not resolved to a Node argument array", cmd.Args)
	}
}
