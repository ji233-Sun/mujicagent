package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignUpdate(t *testing.T) {
	t.Chdir(t.TempDir())
	pub, key, _ := ed25519.GenerateKey(nil)
	t.Setenv("MYGO_UPDATER_PRIVATE_KEY", base64.StdEncoding.EncodeToString(key))
	writeTestFile(t, "mygo.json", fmt.Sprintf(`{"version":"1.2.3","updates":{"publicKey":%q}}`, base64.StdEncoding.EncodeToString(pub)))
	writeTestFile(t, "app.tar.gz", "final archive including stapled ticket")
	manifest := `{"version":"1.2.3","url":"https://example.com/app.tar.gz","notes":"keep","date":"keep","size":1,"signature":"old"}`
	writeTestFile(t, "update.json", manifest)
	if err := signUpdate("update.json", "app.tar.gz"); err != nil {
		t.Fatal(err)
	}
	verifyTestSignature(t, "update.json", "app.tar.gz", pub)
	for _, bad := range []string{
		strings.Replace(manifest, "1.2.3", "1.2.4", 1),
		strings.Replace(manifest, "app.tar.gz", "other.tar.gz", 1),
		strings.Replace(manifest, `"size":1`, `"deltas":[{}],"size":1`, 1),
	} {
		writeTestFile(t, "update.json", bad)
		if err := signUpdate("update.json", "app.tar.gz"); err == nil {
			t.Fatal("accepted incompatible manifest")
		}
	}
	writeTestFile(t, "update.json", manifest)
	_, wrong, _ := ed25519.GenerateKey(nil)
	t.Setenv("MYGO_UPDATER_PRIVATE_KEY", base64.StdEncoding.EncodeToString(wrong))
	if err := signUpdate("update.json", "app.tar.gz"); err == nil {
		t.Fatal("accepted wrong update key")
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
}

func verifyTestSignature(t *testing.T, manifest, archive string, pub ed25519.PublicKey) {
	t.Helper()
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Signature, Notes, Date string
		Size                   int64
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	sig, _ := base64.StdEncoding.DecodeString(m.Signature)
	if !ed25519.Verify(pub, digest[:], sig) || m.Size != int64(len(payload)) || m.Notes != "keep" || m.Date != "keep" {
		t.Fatal("final archive signature, size or preserved metadata is incorrect")
	}
}

func TestNotarizationResume(t *testing.T) {
	source, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	store := filepath.Join(root, "artifacts")
	bin := filepath.Join(root, "bin")
	pub, key, _ := ed25519.GenerateKey(nil)
	build := filepath.Join(store, "release-build")
	writeTestFile(t, filepath.Join(build, "mygo.json"), fmt.Sprintf(`{"version":"1.2.3","updates":{"publicKey":%q}}`, base64.StdEncoding.EncodeToString(pub)))
	writeTestFile(t, filepath.Join(build, "CHANGELOG.md"), "# Changelog")
	writeTestFile(t, filepath.Join(build, "state.json"), `{"source":"source-sha","run":"123","version":"1.2.3"}`)
	platform := filepath.Join(root, "fixture", "darwin-arm64")
	writeTestFile(t, filepath.Join(platform, "MujicAgent.app/Contents/MacOS/app"), "signed executable")
	writeTestFile(t, filepath.Join(platform, "mujicagent-1.2.3-arm64.dmg"), "signed dmg")
	writeTestFile(t, filepath.Join(platform, "update-darwin-arm64.json"), `{"version":"1.2.3","url":"https://example.com/mujicagent-1.2.3-darwin-arm64.tar.gz","notes":"keep","date":"keep","size":1,"signature":"old"}`)
	cmd := exec.Command("tar", "-czf", filepath.Join(build, "arm64.tar.gz"), "-C", filepath.Join(root, "fixture"), "darwin-arm64")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var sums strings.Builder
	for _, name := range []string{"mygo.json", "CHANGELOG.md", "state.json", "arm64.tar.gz"} {
		data, _ := os.ReadFile(filepath.Join(build, name))
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(data), name)
	}
	writeTestFile(t, filepath.Join(build, "SHA256SUMS"), sums.String())
	writeTestFile(t, filepath.Join(bin, "gh"), `#!/bin/bash
set -eu
if [[ "$1" == api ]]; then
  [[ "${API_FAIL:-0}" == 0 ]] || exit 42
  for name in release-build release-submission-arm64 release-final-arm64; do
    if [[ "$*" == *"\"$name\""* && -d "$STORE/$name" ]]; then echo false; fi
  done
else
  while (( $# )); do
    case "$1" in --name) name="$2"; shift;; --dir) dest="$2"; shift;; esac
    shift
  done
  mkdir -p "$dest"
  cp -R "$STORE/$name/." "$dest/"
fi
`)
	writeTestFile(t, filepath.Join(bin, "xcrun"), `#!/bin/bash
set -eu
echo "$1 $2" >> "$CALLS"
case "$1 $2" in
  "notarytool submit") echo '{"id":"11111111-1111-1111-1111-111111111111"}';;
  "notarytool wait") echo "{\"status\":\"${NOTARY_STATUS:-Accepted}\"}";;
  "notarytool log") echo '{}' > "${!#}";;
  "stapler staple")
    if [[ "$3" == *.app ]]; then echo ticket > "$3/Contents/CodeResources"; fi;;
  "stapler validate")
    if [[ "$3" == *.app ]]; then test -s "$3/Contents/CodeResources"; fi;;
esac
`)
	writeTestFile(t, filepath.Join(bin, "codesign"), "#!/bin/bash\nexit 0\n")
	goPath, _ := exec.LookPath("go")
	writeTestFile(t, filepath.Join(bin, "go"), fmt.Sprintf("#!/bin/bash\nshift 2\nexec %q run %q %q \"$@\"\n", goPath, filepath.Join(source, "main.go"), filepath.Join(source, "sign.go")))
	calls := filepath.Join(root, "calls")
	runStage := func(stage string, extra ...string) (string, error) {
		t.Helper()
		work := t.TempDir()
		for _, name := range []string{"macos.sh", "retry-apple.sh"} {
			data, err := os.ReadFile(filepath.Join(source, name))
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(work, "scripts/release", name), string(data))
		}
		cmd := exec.Command("bash", "scripts/release/macos.sh", stage)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
			"STORE="+store, "CALLS="+calls, "GITHUB_SHA=source-sha", "GITHUB_RUN_ID=123",
			"GITHUB_REPOSITORY=owner/repo", "RUNNER_TEMP="+work, "GITHUB_OUTPUT="+filepath.Join(work, "output"),
			"ARCH=arm64", "APPLE_API_KEY_ID=test", "APPLE_API_ISSUER_ID=test", "APPLE_API_PRIVATE_KEY=test",
			"MYGO_UPDATER_PRIVATE_KEY="+base64.StdEncoding.EncodeToString(key))
		cmd.Env = append(cmd.Env, extra...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("%s: %v\n%s", stage, err, out)
		}
		return work, err
	}
	first, err := runStage("submit")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(store, "release-submission-arm64"), os.DirFS(filepath.Join(first, "submit-state"))); err != nil {
		t.Fatal(err)
	}
	if _, err := runStage("submit"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(calls)
	if strings.Count(string(data), "notarytool submit") != 1 {
		t.Fatal("re-submitted a persisted submission")
	}
	if _, err := runStage("submit", "GITHUB_SHA=wrong-source"); err == nil {
		t.Fatal("accepted mismatched source")
	}
	if _, err := runStage("submit", "API_FAIL=1"); err == nil {
		t.Fatal("treated API failure as absent checkpoint")
	}
	if _, err := runStage("finalize", "NOTARY_STATUS=Invalid"); err == nil {
		t.Fatal("accepted rejected notarization")
	}
	data, _ = os.ReadFile(calls)
	if strings.Contains(string(data), "stapler") {
		t.Fatal("stapled rejected submission")
	}
	final, err := runStage("finalize")
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(final, "final-state/mujicagent-1.2.3-darwin-arm64.tar.gz")
	verifyTestSignature(t, filepath.Join(final, "final-state/update-darwin-arm64.json"), archive, pub)
	f, _ := os.Open(archive)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	found := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == "MujicAgent.app/Contents/CodeResources" {
			found = true
		}
	}
	if !found {
		t.Fatal("final update lost its stapled ticket")
	}
	if err := os.CopyFS(filepath.Join(store, "release-final-arm64"), os.DirFS(filepath.Join(final, "final-state"))); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(calls)
	if _, err := runStage("finalize"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(calls)
	if string(before) != string(after) {
		t.Fatal("retried completed notarization")
	}
}

func TestPublishResume(t *testing.T) {
	source, _ := os.Getwd()
	root := t.TempDir()
	origin, seed := filepath.Join(root, "origin.git"), filepath.Join(root, "seed")
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(root, "init", "--bare", origin)
	git(root, "init", "-b", "main", seed)
	git(seed, "remote", "add", "origin", origin)
	for _, name := range []string{"macos.sh", "retry-apple.sh"} {
		data, _ := os.ReadFile(filepath.Join(source, name))
		writeTestFile(t, filepath.Join(seed, "scripts/release", name), string(data))
	}
	writeTestFile(t, filepath.Join(seed, "mygo.json"), `{"version":"1.2.2"}`)
	writeTestFile(t, filepath.Join(seed, "CHANGELOG.md"), "old")
	git(seed, "add", ".")
	git(seed, "commit", "-m", "initial")
	git(seed, "push", "origin", "main")
	sha := git(seed, "rev-parse", "HEAD")
	store := filepath.Join(root, "store")
	for _, name := range []string{"release-build", "release-final-arm64", "release-final-amd64"} {
		dir := filepath.Join(store, name)
		files := map[string]string{"test.dmg": "dmg", "test.tar.gz": "archive", "update-test.json": "manifest"}
		if name == "release-build" {
			files = map[string]string{"mygo.json": `{"version":"1.2.3"}`, "CHANGELOG.md": "new", "release-notes.md": "notes", "state.json": fmt.Sprintf(`{"source":%q,"run":"123"}`, sha)}
		}
		var sums strings.Builder
		for path, data := range files {
			writeTestFile(t, filepath.Join(dir, path), data)
			fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256([]byte(data)), path)
		}
		writeTestFile(t, filepath.Join(dir, "SHA256SUMS"), sums.String())
	}
	bin := filepath.Join(root, "bin")
	writeTestFile(t, filepath.Join(bin, "gh"), `#!/bin/bash
set -eu
case "$1" in
api)
  if [[ "$*" == *"/artifacts?"* ]]; then echo false
  elif [[ -f "$STORE/draft" ]]; then cat "$STORE/draft"; fi;;
run)
  while (( $# )); do
    case "$1" in --name) name="$2"; shift;; --dir) dest="$2"; shift;; esac
    shift
  done
  mkdir -p "$dest"
  cp -R "$STORE/$name/." "$dest/";;
release)
  echo "$2" >> "$STORE/actions"
  case "$2" in
    create) echo true > "$STORE/draft";;
    upload)
      if [[ ! -f "$STORE/upload-attempted" ]]; then touch "$STORE/upload-attempted"; exit 42; fi;;
    edit) echo false > "$STORE/draft";;
  esac;;
esac
`)
	run := func() error {
		work := filepath.Join(t.TempDir(), "checkout")
		git(root, "clone", origin, work)
		git(work, "checkout", sha)
		cmd := exec.Command("bash", "scripts/release/macos.sh", "publish")
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
			"STORE="+store, "GITHUB_SHA="+sha, "GITHUB_RUN_ID=123", "GITHUB_REPOSITORY=owner/repo")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("publish: %v\n%s", err, out)
		}
		return err
	}
	if err := run(); err == nil {
		t.Fatal("expected interrupted upload")
	}
	releaseSHA := git(root, "--git-dir="+origin, "rev-parse", "main")
	if releaseSHA == sha {
		t.Fatal("release commit not created")
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	if git(root, "--git-dir="+origin, "rev-parse", "main") != releaseSHA {
		t.Fatal("duplicate release commit")
	}
	before, _ := os.ReadFile(filepath.Join(store, "actions"))
	if string(before) != "create\nupload\nupload\nedit\n" {
		t.Fatalf("actions: %s", before)
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(store, "actions"))
	if string(before) != string(after) {
		t.Fatal("mutated published release")
	}
}

func TestRetryAppleTicket(t *testing.T) {
	const validate = "The validate action failed! Error 68."
	const staple = "The staple and validate action failed!"
	const network = "Error Domain=NSURLErrorDomain Code=-1005"
	for _, tc := range []struct {
		name, marker, message   string
		failures, status, calls int
	}{
		{"success", validate, "", 0, 0, 1},
		{"validate recovers", validate, network + "\n" + validate, 1, 0, 2},
		{"staple recovers", staple, staple + " Error 68\n" + network, 1, 0, 2},
		{"timeout recovers", validate, "NSURLErrorDomain Code=-1001\n" + validate, 1, 0, 2},
		{"network exhausted", validate, network + "\n" + validate, 5, 68, 5},
		{"non-network error 68", validate, validate, 3, 68, 1},
		{"unrelated network error", staple, network, 3, 68, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command("bash", "-e", "-c", `
source "./retry-apple.sh"
sleep() { echo "$1" >> "$RUNNER_TEMP/delays"; }
operation() {
  [[ "$1" == 'App with spaces.app' ]] || return 99
  echo attempt >> "$RUNNER_TEMP/calls"
  count=$(wc -l < "$RUNNER_TEMP/calls")
  if (( count <= FAILURES )); then
    printf '%s\n' "$MESSAGE" >&2
    return 68
  fi
}
retry_apple_ticket "$MARKER" operation 'App with spaces.app'
echo complete
`)
			cmd.Env = append(os.Environ(), "RUNNER_TEMP="+dir, "MARKER="+tc.marker,
				"MESSAGE="+tc.message, fmt.Sprintf("FAILURES=%d", tc.failures))
			out, err := cmd.CombinedOutput()
			status := 0
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					status = exit.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			calls, err := os.ReadFile(filepath.Join(dir, "calls"))
			if err != nil {
				t.Fatal(err)
			}
			if status != tc.status || strings.Count(string(calls), "attempt") != tc.calls ||
				strings.Contains(string(out), "complete") != (tc.status == 0) {
				t.Fatalf("status=%d calls=%q output=%s", status, calls, out)
			}
			delays, _ := os.ReadFile(filepath.Join(dir, "delays"))
			want := []string{"", "10\n", "10\n20\n", "10\n20\n40\n", "10\n20\n40\n60\n"}[tc.calls-1]
			if string(delays) != want {
				t.Fatalf("delays=%q, want %q", delays, want)
			}
		})
	}
}

func TestNextVersion(t *testing.T) {
	for bump, want := range map[string]string{"patch": "1.2.4", "minor": "1.3.0", "major": "2.0.0"} {
		got, err := nextVersion("1.2.3", bump)
		if err != nil || got != want {
			t.Fatalf("%s: %q, %v", bump, got, err)
		}
	}
	for _, version := range []string{"1.2", "v1.2.3", "01.2.3", "1.2.3-rc1", "1.2.3\n"} {
		if _, err := nextVersion(version, "patch"); err == nil {
			t.Fatalf("accepted %q", version)
		}
	}
	if _, err := nextVersion("1.2.3", "other"); err == nil {
		t.Fatal("accepted unknown bump")
	}
}

func TestPrepare(t *testing.T) {
	config := []byte(`{"name":"MujicAgent","version":"0.1.0","updates":{"publicKey":"preserve-me"}}`)
	history := []byte("# Changelog\n\n## 0.1.0\n\nOld notes.\n")
	updated, log, err := prepare(config, history, "0.1.1", "## What's Changed\n\n- 新增自动更新\n")
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(updated, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["version"]) != `"0.1.1"` || !strings.Contains(string(fields["updates"]), "preserve-me") {
		t.Fatal("config fields were lost")
	}
	want := "# Changelog\n\n## 0.1.1\n\n#### What's Changed\n\n- 新增自动更新\n\n## 0.1.0\n\nOld notes.\n"
	if string(log) != want {
		t.Fatalf("changelog: %s", log)
	}
	if _, _, err := prepare(updated, log, "0.1.1", "Duplicate"); err == nil {
		t.Fatal("duplicate release accepted")
	}
	if _, _, err := prepare(config, history, "0.1.1", " "); err == nil {
		t.Fatal("empty notes accepted")
	}
}
