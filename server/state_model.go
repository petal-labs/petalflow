package server

import "github.com/petal-labs/petalflow/hydrate"

type serverState struct {
	AuthUser     *authAccount                `json:"auth_user,omitempty"`
	Settings     AppSettings                 `json:"settings"`
	Providers    hydrate.ProviderMap         `json:"providers,omitempty"`
	ProviderMeta map[string]providerMetadata `json:"provider_meta,omitempty"`
}
