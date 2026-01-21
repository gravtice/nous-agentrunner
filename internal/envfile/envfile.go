package envfile

import (
	"bufio"
	"errors"
	"os"
	"strings"
)

// LoadFirst loads the first existing env file from paths in order.
// It returns the loaded path (or empty) and the parsed key-values.
func LoadFirst(paths []string) (string, map[string]string, error) {
	for _, path := range paths {
		kv, err := Load(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		return path, kv, err
	}
	return "", map[string]string{}, nil
}

func Load(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := make(map[string]string)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		if key == "" {
			continue
		}
		out[key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
