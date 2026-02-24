package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/petal-labs/petalflow/hydrate"
)

func normalizeProviderTypeName(providerType ProviderType) string {
	return strings.ToLower(strings.TrimSpace(string(providerType)))
}

func (s *Server) ensureRuntimeProviderType(providerType ProviderType) {
	name := normalizeProviderTypeName(providerType)
	if name == "" {
		return
	}

	s.providersMu.Lock()
	defer s.providersMu.Unlock()

	if s.providers == nil {
		s.providers = hydrate.ProviderMap{}
	}
	if _, exists := s.providers[name]; !exists {
		s.providers[name] = hydrate.ProviderConfig{}
	}
}

func (s *Server) setRuntimeProviderAPIKey(providerType ProviderType, apiKey string) {
	name := normalizeProviderTypeName(providerType)
	if name == "" {
		return
	}

	s.providersMu.Lock()
	defer s.providersMu.Unlock()

	if s.providers == nil {
		s.providers = hydrate.ProviderMap{}
	}

	cfg := s.providers[name]
	cfg.APIKey = strings.TrimSpace(apiKey)
	s.providers[name] = cfg
}

func (s *Server) snapshotRuntimeProviders() hydrate.ProviderMap {
	s.providersMu.RLock()
	defer s.providersMu.RUnlock()

	return cloneProviderMap(s.providers)
}

func (s *Server) resolveRunProviders(ctx context.Context) (hydrate.ProviderMap, error) {
	providers := s.snapshotRuntimeProviders()
	if s.providerStore == nil {
		return providers, nil
	}

	records, err := s.providerStore.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing providers: %w", err)
	}

	for _, rec := range records {
		name := normalizeProviderTypeName(rec.Type)
		if name == "" {
			continue
		}

		cfg, exists := providers[name]
		if !exists {
			cfg = hydrate.ProviderConfig{}
		}

		if strings.TrimSpace(cfg.APIKey) == "" {
			apiKey, err := s.providerStore.GetAPIKey(ctx, rec.ID)
			if err != nil {
				if errors.Is(err, ErrProviderNotFound) {
					providers[name] = cfg
					continue
				}
				return nil, fmt.Errorf("loading provider api key for %q: %w", rec.ID, err)
			}
			cfg.APIKey = strings.TrimSpace(apiKey)
		}

		providers[name] = cfg
	}

	return providers, nil
}
