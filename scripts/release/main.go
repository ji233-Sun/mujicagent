// Command release prepares versioned files and signs finalized update archives.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var stableVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 3 && args[0] == "sign-update" {
		return signUpdate(args[1], args[2])
	}
	config, err := os.ReadFile("mygo.json")
	if err != nil {
		return err
	}
	var current struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(config, &current); err != nil {
		return err
	}
	if (len(args) == 2 || len(args) == 3) && args[0] == "next" {
		if len(args) == 3 {
			current.Version = args[2]
		}
		next, err := nextVersion(current.Version, args[1])
		if err != nil {
			return err
		}
		fmt.Println(next)
		return nil
	}
	if len(args) != 3 || args[0] != "prepare" {
		return fmt.Errorf("usage: release next patch|minor|major [BASE_VERSION], release prepare VERSION NOTES_FILE, or release sign-update MANIFEST ARCHIVE")
	}
	notes, err := os.ReadFile(args[2])
	if err != nil {
		return err
	}
	changelog, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		return err
	}
	updatedConfig, updatedLog, err := prepare(config, changelog, args[1], string(notes))
	if err != nil {
		return err
	}
	if err := os.WriteFile("mygo.json", updatedConfig, 0644); err != nil {
		return err
	}
	return os.WriteFile("CHANGELOG.md", updatedLog, 0644)
}

func nextVersion(current, bump string) (string, error) {
	match := stableVersion.FindStringSubmatch(current)
	if match == nil {
		return "", fmt.Errorf("invalid stable version %q", current)
	}
	var parts [3]uint64
	for i := range parts {
		n, err := strconv.ParseUint(match[i+1], 10, 32)
		if err != nil {
			return "", err
		}
		parts[i] = n
	}
	switch bump {
	case "major":
		parts = [3]uint64{parts[0] + 1, 0, 0}
	case "minor":
		parts[1]++
		parts[2] = 0
	case "patch":
		parts[2]++
	default:
		return "", fmt.Errorf("invalid bump %q", bump)
	}
	return fmt.Sprintf("%d.%d.%d", parts[0], parts[1], parts[2]), nil
}

func prepare(config, changelog []byte, version, notes string) ([]byte, []byte, error) {
	if !stableVersion.MatchString(version) {
		return nil, nil, fmt.Errorf("invalid stable version %q", version)
	}
	notes = strings.TrimSpace(notes)
	if notes == "" {
		return nil, nil, fmt.Errorf("release notes must not be empty")
	}
	// MyGo delimits versions with level-two headings; nest generated GitHub headings.
	lines := strings.Split(notes, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "## ") {
			lines[i] = "##" + line
		}
	}
	notes = strings.Join(lines, "\n")
	for _, line := range strings.Split(string(changelog), "\n") {
		if strings.TrimSpace(line) == "## "+version {
			return nil, nil, fmt.Errorf("version %s already has release notes", version)
		}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(config, &fields); err != nil {
		return nil, nil, err
	}
	if fields == nil {
		return nil, nil, fmt.Errorf("mygo.json must be an object")
	}
	fields["version"], _ = json.Marshal(version)
	updated, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	history := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(changelog)), "# Changelog"))
	log := "# Changelog\n\n## " + version + "\n\n" + notes + "\n\n" + history + "\n"
	return append(updated, '\n'), []byte(log), nil
}
