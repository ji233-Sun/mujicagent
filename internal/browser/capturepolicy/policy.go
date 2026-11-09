// Package capturepolicy pins the trusted policy and native implementation
// together, so a stale generated helper cannot satisfy a newer executable.
package capturepolicy

import (
	"crypto/sha256"
	"embed"
)

//go:embed native/capture.js native/capture.cpp native/webkit_abi.h
var sources embed.FS

func Source() string {
	b, _ := sources.ReadFile("native/capture.js")
	return string(b)
}

func Digest(pageScript string) [32]byte {
	h := sha256.New()
	for _, name := range []string{"native/capture.js", "native/capture.cpp", "native/webkit_abi.h"} {
		b, _ := sources.ReadFile(name)
		h.Write(b)
		h.Write([]byte{0})
	}
	h.Write([]byte(pageScript))
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}
