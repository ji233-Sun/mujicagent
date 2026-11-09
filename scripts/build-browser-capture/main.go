// Build the opt-in system-WebKit injected bundle. Go still builds without cgo.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ji233-Sun/mujicagent/internal/browser/capturepolicy"
)

func main() {
	if runtime.GOOS != "darwin" {
		return
	}
	if err := build(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func build() error {
	source := filepath.Join("internal", "browser", "capturepolicy", "native")
	policy := capturepolicy.Source()
	page, err := os.ReadFile(filepath.Join("internal", "browser", "page.js"))
	if err != nil {
		return err
	}
	if strings.Contains(policy, ")MCAP\"") || strings.Contains(string(page), ")MCAP\"") {
		return fmt.Errorf("capture policy contains raw string delimiter")
	}
	if err := os.MkdirAll("build", 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Join("build"), "capture-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	header := "static constexpr char capturePolicy[] = R\"MCAP(" + policy + ")MCAP\";\n"
	header += "static constexpr char pageSource[] = R\"MCAP(" + string(page) + ")MCAP\";\nstatic constexpr unsigned char captureDigest[] = {"
	for _, b := range capturepolicy.Digest(string(page)) {
		header += fmt.Sprintf("%d,", b)
	}
	header += "};\n"
	if err := os.WriteFile(filepath.Join(stage, "capture_policy.h"), []byte(header), 0644); err != nil {
		return err
	}
	bundle := filepath.Join(stage, "MujicAgentCapture.bundle")
	binary := filepath.Join(bundle, "Contents", "MacOS", "MujicAgentCapture")
	if err := os.MkdirAll(filepath.Dir(binary), 0755); err != nil {
		return err
	}
	const plist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>MujicAgentCapture</string>
<key>CFBundleIdentifier</key><string>dev.ji233.mujicagent.capture</string>
<key>CFBundlePackageType</key><string>BNDL</string>
<key>CFBundleVersion</key><string>1</string>
</dict></plist>
`
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(plist), 0644); err != nil {
		return err
	}
	cmd := exec.Command("xcrun", "clang++", "-std=c++17", "-bundle", "-fvisibility=hidden",
		"-arch", "arm64", "-arch", "x86_64", "-mmacosx-version-min=12.0",
		"-framework", "JavaScriptCore", "-framework", "WebKit", "-framework", "ImageIO",
		"-framework", "CoreGraphics", "-framework", "CoreFoundation",
		"-I", stage, filepath.Join(source, "capture.cpp"), "-o", binary)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	cmd = exec.Command("codesign", "--force", "--sign", "-", bundle)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	destination := filepath.Join("resources", "darwin", "MujicAgentCapture.bundle")
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	// Only this generated bundle is replaced, never other resources.
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	return os.Rename(bundle, destination)
}
