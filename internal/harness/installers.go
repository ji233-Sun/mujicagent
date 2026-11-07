package harness

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/ji233-Sun/mujicagent/internal/codex"
)

// An installer supplies identity, real distribution channels and protocol probes.
// Ownership, planning, execution and UI are shared by every registered driver.
type installerAdapter struct {
	packageName, entry, cask, versionPrefix string
	sources, helpArgs                       []string
	helpContains                            string
	latest                                  func(context.Context, string) (Release, error)
}

const piPackage = "@earendil-works/pi-coding-agent"
const droidPackage = "@factory/cli"
const piEntry = "dist/bundle/cli.js"
const droidEntry = "bin/droid"

var installerAdapters = map[string]installerAdapter{
	"codex": {packageName: "@openai/codex", entry: "bin/codex.js", cask: "codex", versionPrefix: "codex-cli ", sources: []string{"", "cn", "native-mirror"}, helpArgs: []string{"app-server", "--help"}, latest: codexLatest},
	"pi":    {packageName: piPackage, entry: piEntry, sources: []string{"", "npm", "cn"}, helpArgs: []string{"--help"}, helpContains: "--mode", latest: piLatest},
	"droid": {packageName: droidPackage, entry: droidEntry, cask: "droid", sources: []string{"", "npm", "cn", "native-mirror"}, helpArgs: []string{"exec", "--help"}, helpContains: "stream-jsonrpc", latest: droidLatest},
}

func InstallSources(driver string) []string { return slices.Clone(installerAdapters[driver].sources) }

func UpdateSource(i Installation) string {
	if i.Method == "npm" || i.Method == "bun" || i.Method == "pnpm" || i.Method == "vp" {
		if i.Driver != "codex" {
			return "npm"
		}
	}
	return i.Source
}

func Latest(ctx context.Context, driver, source string) (Release, error) {
	if source == "official" {
		source = ""
	}
	a, ok := installerAdapters[driver]
	if !ok || !validSource(driver, source) {
		return Release{}, fmt.Errorf("unsupported installation: %s/%s", driver, source)
	}
	return a.latest(ctx, source)
}

func installationVersion(driver string, output []byte) (string, error) {
	a, ok := installerAdapters[driver]
	value := strings.TrimSpace(string(output))
	if !ok || !strings.HasPrefix(value, a.versionPrefix) {
		return "", fmt.Errorf("unrecognized %s version", driver)
	}
	version := strings.TrimPrefix(value, a.versionPrefix)
	if !versionPattern.MatchString(version) {
		return "", fmt.Errorf("unrecognized %s version", driver)
	}
	return version, nil
}

func verify(ctx context.Context, driver, path, version string) (string, error) {
	if driver == "" {
		driver = "codex"
	}
	cmd, err := codex.Command(ctx, path, "--version")
	if err != nil {
		return "", err
	}
	prepareProcess(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	actual, err := installationVersion(driver, out)
	if err != nil || actual != version {
		return "", fmt.Errorf("installed %s version does not match %s", driver, version)
	}
	a := installerAdapters[driver]
	cmd, err = codex.Command(ctx, path, a.helpArgs...)
	if err != nil {
		return "", err
	}
	prepareProcess(cmd)
	out, err = cmd.Output()
	if err != nil || !strings.Contains(string(out), a.helpContains) {
		return "", fmt.Errorf("%s session protocol unavailable: %v", driver, err)
	}
	return path, nil
}

func classifyDriver(driver, path, managed string) (string, string) {
	if driver == "codex" {
		return classify(path, managed)
	}
	if rel, err := filepath.Rel(managed, path); err == nil && filepath.IsLocal(rel) {
		return "managed", ""
	}
	a := installerAdapters[driver]
	slash := filepath.ToSlash(path)
	if a.cask != "" {
		if i := strings.Index(slash, "/Caskroom/"+a.cask+"/"); i >= 0 {
			return "brew", filepath.FromSlash(slash[:i])
		}
	}
	for _, entry := range []string{a.entry, "dist/cli.js"} {
		suffix := "/lib/node_modules/" + a.packageName + "/" + entry
		if strings.HasSuffix(slash, suffix) {
			return "npm", filepath.FromSlash(strings.TrimSuffix(slash, suffix))
		}
	}
	if runtime.GOOS == "windows" {
		if i := strings.Index(slash, "/node_modules/"+a.packageName+"/"); i > 0 {
			return "npm", filepath.FromSlash(slash[:i])
		}
	}
	return "custom", ""
}

// Native self-updaters are offered only for an identified official layout.
// App bundles and Nix store paths remain owned by their original installer.
func inspectNative(ctx context.Context, current Installation, real string) Installation {
	slash := filepath.ToSlash(real)
	if strings.Contains(slash, "/nix/store/") {
		current.Method, current.UpdateIssue = "nix", "unsupported"
		return current
	}
	if current.Driver == "pi" {
		launcher := canonical(current.Path)
		root := filepath.Join(filepath.Dir(filepath.Dir(launcher)), "install")
		var marker struct {
			Kind, Layout  string
			SchemaVersion int
			Entrypoint    struct{ Path string }
		}
		data, err := readSmallFile(filepath.Join(root, "managed-install.json"))
		if err == nil && json.Unmarshal(data, &marker) == nil && marker.Kind == "pi-managed-install" && marker.Layout == "releases-v1" && marker.SchemaVersion == 1 && canonical(marker.Entrypoint.Path) == launcher {
			current.Method = "native"
		}
	}
	if current.Driver == "droid" {
		if strings.Contains(slash, "/Factory.app/Contents/") {
			current.Method, current.UpdateIssue = "Factory.app", "unsupported"
			return current
		}
		home, _ := os.UserHomeDir()
		exe := "droid"
		if runtime.GOOS == "windows" {
			exe += ".exe"
		}
		if home == "" || real != filepath.Join(home, ".local", "bin", exe) {
			return current
		}
		arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
		f, err := os.Open(real)
		if err != nil {
			return current
		}
		h := sha256.New()
		_, err = io.Copy(h, io.LimitReader(f, 512<<20))
		f.Close()
		if err != nil {
			return current
		}
		arches := []string{arch}
		if arch == "x64" {
			arches = append(arches, "x64-baseline")
		}
		for _, cpu := range arches {
			digest, err := getText(ctx, "https://downloads.factory.ai/factory-cli/releases/"+current.Version+"/"+runtime.GOOS+"/"+cpu+"/"+exe+".sha256")
			if err == nil && strings.EqualFold(digest, hex.EncodeToString(h.Sum(nil))) {
				current.Method = "native"
				break
			}
		}
	}
	return current
}

func npmRelease(ctx context.Context, packageName, entry, source string) (Release, error) {
	var meta struct {
		Version string
		Dist    struct{ Tarball, Integrity string }
	}
	if err := getJSON(ctx, registry(source)+"/"+url.PathEscape(packageName)+"/latest", &meta); err != nil {
		return Release{}, err
	}
	if !versionPattern.MatchString(meta.Version) {
		return Release{}, fmt.Errorf("invalid %s release version", packageName)
	}
	return Release{meta.Version, meta.Dist.Tarball, meta.Dist.Integrity, "package/" + entry}, nil
}

func piLatest(ctx context.Context, source string) (Release, error) {
	if source != "" {
		return npmRelease(ctx, piPackage, piEntry, source)
	}
	var release struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name, Digest string
			URL          string `json:"browser_download_url"`
		}
	}
	if err := getJSON(ctx, "https://api.github.com/repos/earendil-works/pi/releases/latest", &release); err != nil {
		return Release{}, err
	}
	version := strings.TrimPrefix(release.Tag, "v")
	if !versionPattern.MatchString(version) {
		return Release{}, fmt.Errorf("invalid Pi release version")
	}
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	ext, entry := ".tar.gz", "pi/pi"
	if runtime.GOOS == "windows" {
		ext, entry = ".zip", "pi.exe"
	}
	name := "pi-" + runtime.GOOS + "-" + arch + ext
	for _, asset := range release.Assets {
		if asset.Name == name && asset.Digest != "" {
			return Release{version, asset.URL, asset.Digest, entry}, nil
		}
	}
	return Release{}, fmt.Errorf("Pi package unavailable for %s/%s", runtime.GOOS, runtime.GOARCH)
}

func droidLatest(ctx context.Context, source string) (Release, error) {
	if source == "npm" {
		return npmRelease(ctx, droidPackage, droidEntry, source)
	}
	arch := map[string]string{"amd64": "x64-baseline", "arm64": "arm64"}[runtime.GOARCH]
	if arch == "" {
		return Release{}, fmt.Errorf("unsupported Droid architecture")
	}
	exe := "droid"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if source == "cn" || source == "native-mirror" {
		base, err := npmRelease(ctx, droidPackage, droidEntry, source)
		if err != nil {
			return Release{}, err
		}
		platform := runtime.GOOS
		if platform == "windows" {
			platform = "win32"
		}
		var native struct {
			Dist struct{ Tarball, Integrity string }
		}
		if err := getJSON(ctx, registry(source)+"/@factory%2Fcli-"+platform+"-"+arch+"/"+base.Version, &native); err != nil {
			return Release{}, fmt.Errorf("Droid %s mirror platform package unavailable; retry or select an official channel: %w", base.Version, err)
		}
		if source == "cn" {
			// npm silently skips unavailable optional platform packages. Reject an
			// incomplete mirror during preview instead of installing a broken shim.
			return base, nil
		}
		return Release{base.Version, native.Dist.Tarball, native.Dist.Integrity, "package/bin/" + exe}, nil
	}
	version, err := getText(ctx, "https://downloads.factory.ai/factory-cli/LATEST")
	if err != nil {
		return Release{}, err
	}
	if !versionPattern.MatchString(version) {
		return Release{}, fmt.Errorf("invalid Droid release version")
	}
	endpoint := "https://downloads.factory.ai/factory-cli/releases/" + version + "/" + runtime.GOOS + "/" + arch + "/" + exe
	digest, err := getText(ctx, endpoint+".sha256")
	if err != nil {
		return Release{}, err
	}
	return Release{version, endpoint, "sha256:" + digest, exe}, nil
}

func getText(ctx context.Context, endpoint string) (string, error) {
	r, err := get(ctx, endpoint)
	if err != nil {
		return "", err
	}
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	return strings.TrimSpace(string(data)), err
}

func safeArchivePath(name string) bool {
	return !strings.ContainsAny(name, `\:`) && filepath.IsLocal(filepath.FromSlash(name))
}

func extractZip(source io.ReaderAt, size int64, root string) error {
	z, err := zip.NewReader(source, size)
	if err != nil {
		return err
	}
	var total uint64
	for _, file := range z.File {
		if !safeArchivePath(file.Name) || file.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe archive path")
		}
		path := filepath.Join(root, filepath.FromSlash(file.Name))
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0700); err != nil {
				return err
			}
			continue
		}
		if !file.Mode().IsRegular() || file.UncompressedSize64 > 2<<30-total {
			return fmt.Errorf("unsupported or oversized archive entry")
		}
		total += file.UncompressedSize64
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		in, err := file.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if err != nil {
			in.Close()
			return err
		}
		_, copyErr := io.Copy(out, io.LimitReader(in, int64(file.UncompressedSize64)+1))
		in.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
