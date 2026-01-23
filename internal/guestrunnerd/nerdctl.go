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
		return nil, fmt.Errorf("nerdctl %v: %s", redactNerdctlArgs(args), msg)
	}
	return stdout.Bytes(), nil
}

func redactNerdctlArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := append([]string(nil), args...)
	for i := 0; i < len(out); i++ {
		switch out[i] {
		case "-e", "--env":
			if i+1 < len(out) {
				out[i+1] = redactEnvKV(out[i+1])
				i++
			}
		default:
			if strings.HasPrefix(out[i], "--env=") {
				out[i] = "--env=" + redactEnvKV(strings.TrimPrefix(out[i], "--env="))
			}
		}
	}
	return out
}

func redactEnvKV(kv string) string {
	key, _, ok := strings.Cut(kv, "=")
	if !ok {
		return "<redacted>"
	}
	return key + "=<redacted>"
}
