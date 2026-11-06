package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// signUpdate refreshes MyGo v0.7.4's manifest after stapling and repackaging.
// MyGo signs the SHA-256 digest, not the archive bytes. Preserve its metadata.
func signUpdate(manifestPath, archivePath string) error {
	config, err := os.ReadFile("mygo.json")
	if err != nil {
		return err
	}
	var c struct {
		Version string `json:"version"`
		Updates struct {
			PublicKey string `json:"publicKey"`
		} `json:"updates"`
	}
	if err := json.Unmarshal(config, &c); err != nil {
		return err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("MYGO_UPDATER_PRIVATE_KEY")))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return fmt.Errorf("invalid update private key")
	}
	pub, err := base64.StdEncoding.DecodeString(c.Updates.PublicKey)
	if err != nil || !bytes.Equal(ed25519.NewKeyFromSeed(key[:ed25519.SeedSize]), key) ||
		!bytes.Equal(ed25519.PrivateKey(key).Public().(ed25519.PublicKey), pub) {
		return fmt.Errorf("update key does not match mygo.json")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	var m struct {
		Version string            `json:"version"`
		URL     string            `json:"url"`
		Deltas  []json.RawMessage `json:"deltas"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	u, err := url.Parse(m.URL)
	if err != nil || u.Scheme != "https" || filepath.Base(u.Path) != filepath.Base(archivePath) || m.Version != c.Version {
		return fmt.Errorf("manifest version or archive does not match")
	}
	if len(m.Deltas) != 0 {
		return fmt.Errorf("repackaging requires updates.deltas=0")
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	fields["size"], _ = json.Marshal(size)
	fields["signature"], _ = json.Marshal(base64.StdEncoding.EncodeToString(ed25519.Sign(key, h.Sum(nil))))
	data, err = json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(manifestPath, append(data, '\n'), 0644)
}
