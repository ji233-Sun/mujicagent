package harness

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestReleaseAndInstallationRouting(t *testing.T) {
	for _, platform := range []string{"darwin", "linux", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			target, exe, err := Target(platform, arch)
			if err != nil || target == "" || (platform == "windows" && exe != "codex.exe") {
				t.Fatal(target, exe, err)
			}
		}
	}
	if _, _, err := Target("windows", "386"); err == nil {
		t.Fatal("accepted unsupported platform")
	}
	for _, tc := range []struct {
		a, b string
		want bool
	}{{"0.167.0", "0.99.0", true}, {"1.0.0", "1.0.0", false}, {"0.99.0", "0.167.0", false}, {"broken", "1.0.0", false}} {
		if Newer(tc.a, tc.b) != tc.want {
			t.Fatal(tc)
		}
	}
	root := filepath.Join(t.TempDir(), "tools")
	for _, tc := range []struct{ path, want string }{{filepath.Join(root, "release-1", "bin", "codex"), "managed"}, {filepath.Join(root+"-other", "codex"), "custom"}, {"/opt/homebrew/Caskroom/codex/1/codex", "brew"}, {"/usr/local/lib/node_modules/@openai/codex/bin/codex.js", "npm"}} {
		method, _ := classify(tc.path, root)
		if method != tc.want {
			t.Fatalf("%s: %s", tc.path, method)
		}
	}
}

func TestExtractPreservesCompanionsAndRejectsUnsafePaths(t *testing.T) {
	for _, name := range []string{"bin/codex", "bin/codex-code-mode-host", "../escape", "/absolute", `C:\escape`, `..\escape`} {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: 4})
		tw.Write([]byte("data"))
		tw.Close()
		gz.Close()
		root := t.TempDir()
		err := extract(&buf, root)
		safe := name == "bin/codex" || name == "bin/codex-code-mode-host"
		if safe {
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
				t.Fatal(err)
			}
		} else if err == nil {
			t.Fatal("accepted", name)
		}
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/tmp"})
	tw.Close()
	gz.Close()
	if err := extract(&buf, t.TempDir()); err == nil {
		t.Fatal("accepted symlink")
	}
}

// Downloads only to a temporary directory; never changes the existing installation.
func TestDownloadNativePackage(t *testing.T) {
	source := os.Getenv("MUJICAGENT_INSTALL_TEST")
	if source == "" {
		t.Skip("explicit download test only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	lookupSource := source
	if source == "cn-npm" {
		lookupSource = "cn"
	}
	release, err := Latest(ctx, lookupSource)
	if err != nil {
		t.Fatal(err)
	}
	installSource := source
	if source == "cn-npm" {
		installSource = "cn"
	}
	if source == "cn" {
		installSource = "native-mirror"
	}
	path, err := Install(ctx, t.TempDir(), installSource, release, Installation{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("verified %s at %s", release.Version, path)
}

func TestAgentProtocol(t *testing.T) {
	for _, tc := range []struct {
		driver, line, want string
		fail               bool
	}{
		{"pi", `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"你好"}}`, "你好", false},
		{"pi", `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`, "done", false},
		{"pi", `{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"bad model"}}`, "", true},
		{"pi", `broken`, "", true},
		{"droid", `{"type":"result","result":"done","session_id":"session-1","is_error":false}`, "done", false},
		{"droid", `{"type":"result","result":"bad login","is_error":true}`, "", true},
		{"droid", `{"type":"result","result":"missing session"}`, "", true},
	} {
		p := agentParser{driver: tc.driver}
		text := ""
		err := p.line([]byte(tc.line), func(e AgentEvent) { text = e.Text })
		if (err != nil) != tc.fail || text != tc.want {
			t.Fatalf("%s: %q %v", tc.line, text, err)
		}
	}
	for _, driver := range []string{"pi", "droid"} {
		args, err := agentArgs(RunOptions{Driver: driver, Session: "owned-session", Model: "custom-model"})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, arg := range args {
			if arg == "custom-model" {
				found = true
			}
			if arg == "--skip-permissions-unsafe" {
				t.Fatal("unsafe default")
			}
		}
		if !found {
			t.Fatal("model omitted")
		}
	}
}

func TestModelCatalogParsing(t *testing.T) {
	pi, err := parseModelCatalog("pi", []byte(`{"models":[{"provider":"a","id":"shared","name":"A","reasoning":true,"thinkingLevelMap":{"low":"x","high":"y"},"headers":{"Authorization":"secret"}},{"provider":"b","id":"shared","name":"B"}]}`))
	if err != nil || len(pi) != 2 || pi[0].ID != "a/shared" || pi[1].ID != "b/shared" || len(pi[0].Efforts) != 3 {
		t.Fatalf("Pi catalog: %+v %v", pi, err)
	}
	droid, err := parseModelCatalog("droid", []byte(`{"models":[{"id":"custom:Mine-0","displayName":"Mine","supportedReasoningEfforts":["low","high"],"defaultReasoningEffort":"high"},{"id":"blocked","disabled":true}]}`))
	if err != nil || len(droid) != 1 || droid[0].ID != "custom:Mine-0" || droid[0].DefaultEffort != "high" {
		t.Fatalf("Droid catalog: %+v %v", droid, err)
	}
	for _, driver := range []string{"pi", "droid"} {
		args, err := agentArgs(RunOptions{Driver: driver, Effort: "high"})
		if err != nil {
			t.Fatal(err)
		}
		flag := "--thinking"
		if driver == "droid" {
			flag = "--reasoning-effort"
		}
		found := false
		for i, arg := range args {
			if arg == flag && i+1 < len(args) && args[i+1] == "high" {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing effort: %v", args)
		}
	}
}

func TestModelDiscoveryProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	for _, driver := range []string{"pi", "droid"} {
		t.Run(driver, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, "agent")
			request := "get_available_models"
			response := `{"id":"models","success":true,"data":{"models":[{"provider":"test","id":"model"}]}}`
			if driver == "droid" {
				request = "droid.list_models"
				response = `{"id":"models","result":{"models":[{"id":"model"}]}}`
			}
			script := "#!/bin/sh\nread request\ncase \"$request\" in\n*" + request + "*) printf '%s\\n' '" + response + "';;\n*) exit 2;;\nesac\nsleep 20\n"
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			models, err := DiscoverModels(ctx, driver, binary, dir)
			if err != nil || len(models) != 1 {
				t.Fatalf("discovery: %+v %v", models, err)
			}
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nsleep 20\n"), 0700); err != nil {
				t.Fatal(err)
			}
			timeout, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer stop()
			if _, err := DiscoverModels(timeout, driver, binary, dir); err == nil {
				t.Fatal("timeout ignored")
			}
		})
	}
}

func TestInstalledModelCatalog(t *testing.T) {
	if os.Getenv("MUJICAGENT_MODEL_DISCOVERY_TEST") != "1" {
		t.Skip("explicit read-only local CLI discovery")
	}
	for _, driver := range []string{"pi", "droid"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		models, err := DiscoverModels(ctx, driver, driver, t.TempDir())
		cancel()
		if err != nil {
			t.Fatal(driver, err)
		}
		t.Logf("%s: %d models", driver, len(models))
	}
}
