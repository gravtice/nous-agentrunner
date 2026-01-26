package runnerd

const (
	protocolVersionASP  = "0.2.0"
	protocolVersionASMP = "0.2.0"
)

func (s *Server) protocolCapabilityFlags() map[string]any {
	return map[string]any{
		"single_ws_per_service":      true,
		"error_fatal_field":          true,
		"invalid_input_returns_done": true,
		"service_idle_timeout":       true,
	}
}

func (s *Server) protocolLimits() map[string]any {
	return map[string]any{
		"max_inline_bytes":     s.cfg.MaxInlineBytes,
		"max_ws_message_bytes": s.maxClientASPMessageBytes(),
	}
}

func (s *Server) protocolCapabilities() map[string]any {
	flags := s.protocolCapabilityFlags()
	limits := s.protocolLimits()
	out := make(map[string]any, len(flags)+len(limits))
	for k, v := range flags {
		out[k] = v
	}
	for k, v := range limits {
		out[k] = v
	}
	return out
}
