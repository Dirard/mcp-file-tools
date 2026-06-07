package handler

import "github.com/Dirard/mcp-file-tools/internal/config"

// Handler handles all file tool operations
type Handler struct {
	config      *config.Config
	limiters    handlerLimiters
	pathLocks   *pathLockManager
	cwdRegistry *cwdRegistry
}

// Option is a functional option for configuring Handler
type Option func(*Handler)

// WithConfig sets the configuration for the handler
func WithConfig(cfg *config.Config) Option {
	return func(h *Handler) {
		if cfg != nil {
			h.config = cfg
		}
	}
}

// NewHandler creates a new Handler with optional configuration.
// If no config is provided via WithConfig, default configuration is used.
func NewHandler(opts ...Option) *Handler {
	h := &Handler{
		config:    config.Load(), // Load defaults from environment
		pathLocks: newPathLockManager(),
	}

	for _, opt := range opts {
		opt(h)
	}
	h.limiters = newHandlerLimiters(h.config)
	h.cwdRegistry = newCwdRegistry(h.config)

	return h
}
