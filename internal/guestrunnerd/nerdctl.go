package guestrunnerd

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func runNerdctl(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "nerdctl", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("nerdctl %v: %s", args, msg)
	}
	return stdout.Bytes(), nil
}
