package runnerd

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type aspMessage struct {
	Type string `json:"type"`
}

type aspInputMessage struct {
	Type     string       `json:"type"`
	Contents []aspContent `json:"contents"`
}

type aspAskAnswerMessage struct {
	Type    string         `json:"type"`
	AskID   string         `json:"ask_id"`
	Answers map[string]any `json:"answers"`
}

type aspContent struct {
	Kind   string     `json:"kind"`
	Text   string     `json:"text,omitempty"`
	Source *aspSource `json:"source,omitempty"`
}

type aspSource struct {
	Type     string `json:"type"`
	Path     string `json:"path,omitempty"`
	Mime     string `json:"mime,omitempty"`
	Encoding string `json:"encoding,omitempty"`
	Data     string `json:"data,omitempty"`
}

func (s *Server) validateClientASPMessage(raw []byte) error {
	var base aspMessage
	if err := json.Unmarshal(raw, &base); err != nil {
		return err
	}
	switch base.Type {
	case "cancel":
		return nil
	case "ask.answer":
		var in aspAskAnswerMessage
		if err := json.Unmarshal(raw, &in); err != nil {
			return err
		}
		if strings.TrimSpace(in.AskID) == "" {
			return errors.New("ask_id is required")
		}
		if in.Answers == nil {
			return errors.New("answers is required")
		}
		return nil
	case "input":
		var in aspInputMessage
		if err := json.Unmarshal(raw, &in); err != nil {
			return err
		}
		if len(in.Contents) == 0 {
			return errors.New("contents is required")
		}
		for _, c := range in.Contents {
			switch c.Kind {
			case "text":
				// ok
			case "image", "audio", "video", "file":
				if c.Source == nil {
					return fmt.Errorf("%s source is required", c.Kind)
				}
				if err := s.validateSource(*c.Source); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unsupported content kind %q", c.Kind)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported message type %q", base.Type)
	}
}

func (s *Server) validateSource(src aspSource) error {
	switch src.Type {
	case "path":
		p := strings.TrimSpace(src.Path)
		if p == "" {
			return errors.New("path is required")
		}
		if _, _, ok := s.validateAllowedPath(p); !ok {
			return errPathNotAllowed
		}
		return nil
	case "bytes":
		if strings.ToLower(strings.TrimSpace(src.Encoding)) != "base64" {
			return errors.New("bytes encoding must be base64")
		}
		decoded, err := base64.StdEncoding.DecodeString(src.Data)
		if err != nil {
			return errors.New("invalid base64 data")
		}
		if int64(len(decoded)) > s.cfg.MaxInlineBytes {
			return errors.New("INLINE_BYTES_TOO_LARGE")
		}
		return nil
	default:
		return fmt.Errorf("unsupported source type %q", src.Type)
	}
}
