package runnerd

import (
	"encoding/base64"
	"encoding/json"
)

func encodeServiceConfig(cfg map[string]any) (string, error) {
	if cfg == nil {
		cfg = map[string]any{}
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}
