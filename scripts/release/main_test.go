package main

import (
	"encoding/json"
	"strings"
	"testing"
)

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
