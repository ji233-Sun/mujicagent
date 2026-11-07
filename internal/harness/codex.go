// Package harness owns driver registration, session protocols and CLI installation.
package harness

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/egoist/mygo/netproxy"
	"github.com/ji233-Sun/mujicagent/internal/codex"
)

type Installation struct{ Driver, Path, Version, Method, Prefix, Source, Manager, PackageRoot, BinDir, UpdateIssue string }
type Release struct{ Version, URL, Digest, Entry string }

var versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func Target(goos, arch string) (string, string, error) {
	cpu := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[arch]
	platform := map[string]string{"darwin": "apple-darwin", "linux": "unknown-linux-musl", "windows": "pc-windows-msvc"}[goos]
	if cpu == "" || platform == "" {
		return "", "", fmt.Errorf("unsupported platform: %s/%s", goos, arch)
	}
	exe := "codex"
	if goos == "windows" {
		exe += ".exe"
	}
	return cpu + "-" + platform, exe, nil
}

func Inspect(ctx context.Context, driver, binary, managed string) (Installation, error) {
	cmd, err := codex.Command(ctx, binary, "--version")
	if err != nil {
		return Installation{}, err
	}
	prepareProcess(cmd)
	out, err := cmd.Output()
	if err != nil {
		return Installation{}, fmt.Errorf("cannot run %s: %w", binary, err)
	}
	version, err := installationVersion(driver, out)
	if err != nil {
		return Installation{}, err
	}
	path := cmd.Path
	if runtime.GOOS == "windows" {
		if launcher, e := exec.LookPath(binary); e == nil {
			path = launcher
		}
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Installation{}, err
	}
	if runtime.GOOS == "windows" {
		if (driver == "pi" || driver == "claude") && len(cmd.Args) > 1 && strings.HasSuffix(cmd.Args[1], ".js") {
			real = canonical(cmd.Args[1])
		}
		if driver == "droid" || driver == "claude" && strings.HasSuffix(cmd.Path, ".exe") && filepath.Base(cmd.Path) != "node.exe" {
			real = canonical(cmd.Path)
		}
	}
	if canonical, err := filepath.EvalSymlinks(managed); err == nil {
		managed = canonical
	}
	method, prefix := classifyDriver(driver, real, managed)
	if runtime.GOOS == "windows" && method == "custom" && strings.EqualFold(filepath.Ext(path), ".cmd") {
		base := filepath.Dir(path)
		if filepath.Base(base) != ".bin" && ownsLauncher(driver, path, real, filepath.Join(base, "node_modules"), base) {
			method, prefix = "npm", base
		}
	}
	// VP dispatches package binaries through symlinks to vp itself. Keep the
	// launcher path: executing its canonical target would run vp, not Codex.
	if method == "custom" && installerAdapters[driver].packageName != "" {
		vp := filepath.Join(filepath.Dir(cmd.Path), "vp"+filepath.Ext(real))
		if filepath.Base(real) == "vp" || filepath.Base(real) == "vp.exe" {
			if target, e := filepath.EvalSymlinks(vp); e == nil && target == real {
				method, prefix = "vp", vp
			}
		}
	}
	current := Installation{Driver: driver, Path: path, Version: version, Method: method, Prefix: prefix}
	if current.Method == "custom" {
		current = inspectNative(ctx, current, real)
	}
	if current.Method == "custom" || current.Method == "npm" {
		current = inspectPackageManager(ctx, current, real)
	}
	if method == "managed" {
		rel, _ := filepath.Rel(managed, real)
		stage := filepath.Join(managed, strings.Split(rel, string(filepath.Separator))[0])
		if data, e := os.ReadFile(filepath.Join(stage, "source.json")); e == nil {
			if e = json.Unmarshal(data, &current.Source); e != nil || !validSource(driver, current.Source) {
				return Installation{}, fmt.Errorf("invalid managed installation source")
			}
		} else if !os.IsNotExist(e) {
			return Installation{}, e
		} else if strings.Contains(filepath.ToSlash(real), "/node_modules/") {
			current.Source = "cn"
		}
	}
	return current, nil
}

func classify(path, managed string) (string, string) {
	rel, err := filepath.Rel(managed, path)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "managed", ""
	}
	slash := filepath.ToSlash(path)
	if i := strings.Index(slash, "/Caskroom/codex/"); i >= 0 {
		return "brew", filepath.FromSlash(slash[:i])
	}
	// Only an identified npm package gets a package-manager update; never guess from PATH.
	const suffix = "/lib/node_modules/@openai/codex/bin/codex.js"
	if strings.HasSuffix(slash, suffix) {
		return "npm", strings.TrimSuffix(path, filepath.FromSlash(suffix))
	}
	if runtime.GOOS == "windows" {
		if i := strings.Index(slash, "/node_modules/@openai/codex"); i > 0 {
			return "npm", filepath.FromSlash(slash[:i])
		}
	}
	return "custom", ""
}

func registry(source string) string {
	if source == "cn" || source == "native-mirror" {
		return "https://registry.npmmirror.com"
	}
	return "https://registry.npmjs.org"
}
func get(ctx context.Context, endpoint string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	if req.URL.Scheme != "https" {
		return nil, fmt.Errorf("download requires HTTPS")
	}
	req.Header.Set("User-Agent", "MujicAgent")
	client := &http.Client{Transport: netproxy.Transport(), CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) > 8 {
			return fmt.Errorf("invalid download redirect")
		}
		return nil
	}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	return resp, nil
}
func getJSON(ctx context.Context, endpoint string, result any) error {
	resp, err := get(ctx, endpoint)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(result)
}

func codexLatest(ctx context.Context, source string) (Release, error) {
	target, exe, err := Target(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Release{}, err
	}
	if source == "cn" {
		var meta struct {
			Version              string
			OptionalDependencies map[string]string
		}
		if err := getJSON(ctx, registry(source)+"/@openai%2Fcodex/latest", &meta); err != nil {
			return Release{}, err
		}
		if !versionPattern.MatchString(meta.Version) {
			return Release{}, fmt.Errorf("invalid release version")
		}
		platform := runtime.GOOS
		if platform == "windows" {
			platform = "win32"
		}
		arch := runtime.GOARCH
		if arch == "amd64" {
			arch = "x64"
		}
		alias := meta.OptionalDependencies["@openai/codex-"+platform+"-"+arch]
		prefix := "npm:@openai/codex@"
		if !strings.HasPrefix(alias, prefix) {
			return Release{}, fmt.Errorf("native npm package unavailable for this platform")
		}
		nativeVersion := strings.TrimPrefix(alias, prefix)
		if nativeVersion != meta.Version+"-"+platform+"-"+arch {
			return Release{}, fmt.Errorf("unexpected platform package")
		}
		var native struct {
			Dist struct{ Tarball, Integrity string }
		}
		if err := getJSON(ctx, registry(source)+"/@openai%2Fcodex/"+nativeVersion, &native); err != nil {
			return Release{}, err
		}
		return Release{meta.Version, native.Dist.Tarball, native.Dist.Integrity, "package/vendor/" + target + "/bin/" + exe}, nil
	}
	var release struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name, Digest string
			URL          string `json:"browser_download_url"`
		}
	}
	if err := getJSON(ctx, "https://api.github.com/repos/openai/codex/releases/latest", &release); err != nil {
		return Release{}, err
	}
	version := strings.TrimPrefix(release.Tag, "rust-v")
	if !versionPattern.MatchString(version) {
		return Release{}, fmt.Errorf("invalid release version")
	}
	for _, a := range release.Assets {
		if a.Name == "codex-package-"+target+".tar.gz" {
			return Release{version, a.URL, a.Digest, "bin/" + exe}, nil
		}
	}
	return Release{}, fmt.Errorf("official package unavailable for %s", target)
}

func Newer(latest, current string) bool {
	var a, b [3]int
	if !versionPattern.MatchString(latest) || !versionPattern.MatchString(current) {
		return false
	}
	fmt.Sscanf(latest, "%d.%d.%d", &a[0], &a[1], &a[2])
	fmt.Sscanf(current, "%d.%d.%d", &b[0], &b[1], &b[2])
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// Install stages a complete distribution beside the active one. Windows never replaces a running exe.
func Install(ctx context.Context, driver, root, source string, release Release, current Installation) (string, error) {
	plan, err := PrepareInstall(ctx, driver, root, source, release, current)
	if err != nil {
		return "", err
	}
	return plan.Execute(ctx)
}

func downloadExtract(ctx context.Context, release Release, stage string) error {
	var h hash.Hash
	var expected []byte
	var err error
	if strings.HasPrefix(release.Digest, "sha256:") {
		h = sha256.New()
		expected, err = hex.DecodeString(strings.TrimPrefix(release.Digest, "sha256:"))
	} else if strings.HasPrefix(release.Digest, "sha512-") {
		h = sha512.New()
		expected, err = base64.StdEncoding.DecodeString(strings.TrimPrefix(release.Digest, "sha512-"))
	} else {
		return fmt.Errorf("package checksum missing")
	}
	if err != nil || len(expected) != h.Size() {
		return fmt.Errorf("invalid package checksum")
	}
	resp, err := get(ctx, release.URL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	archive, err := os.CreateTemp(stage, "archive-")
	if err != nil {
		return err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	const maxArchive = 512 << 20
	n, err := io.Copy(io.MultiWriter(archive, h), io.LimitReader(resp.Body, maxArchive+1))
	if err != nil {
		return err
	}
	if n > maxArchive {
		return fmt.Errorf("archive too large")
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), hex.EncodeToString(expected)) {
		return fmt.Errorf("package checksum mismatch")
	}
	if _, err = archive.Seek(0, 0); err != nil {
		return err
	}
	switch {
	case strings.HasSuffix(release.URL, ".zip"):
		return extractZip(archive, n, stage)
	case strings.HasSuffix(release.URL, ".tar.gz"), strings.HasSuffix(release.URL, ".tgz"):
		return extract(archive, stage)
	default:
		if !safeArchivePath(release.Entry) {
			return fmt.Errorf("unsafe binary path")
		}
		path := filepath.Join(stage, filepath.FromSlash(release.Entry))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, archive)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
}

func extract(source io.Reader, root string) error {
	gz, err := gzip.NewReader(source)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		if name == "." {
			continue
		}
		// Reject Windows drive names, separators and links even when testing on Unix.
		if strings.ContainsAny(name, `\:`) || !filepath.IsLocal(filepath.FromSlash(name)) {
			return fmt.Errorf("unsafe archive path")
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0700); err != nil {
				return err
			}
		case tar.TypeReg:
			total += hdr.Size
			if hdr.Size < 0 || total > 2<<30 {
				return fmt.Errorf("expanded archive too large")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return err
			}
			f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(f, tr)
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("unsupported archive entry: %s", hdr.Name)
		}
	}
}
