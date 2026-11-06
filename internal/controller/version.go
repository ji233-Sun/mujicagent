package controller

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

func Version(binary string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil {
		return "Not installed"
	}
	return strings.TrimSpace(string(b))
}
