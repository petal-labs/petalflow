package cli

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/petal-labs/petalflow/daemon"
	"github.com/petal-labs/petalflow/tool"
)

// NewServeCmd creates the "serve" subcommand.
func NewServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the daemon HTTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			host, _ := cmd.Flags().GetString("host")
			port, _ := cmd.Flags().GetInt("port")
			corsOrigin, _ := cmd.Flags().GetString("cors-origin")
			storeKind, _ := cmd.Flags().GetString("store")
			storePath, _ := cmd.Flags().GetString("store-path")
			explicitConfigPath, _ := cmd.Flags().GetString("config")

			store, err := resolveServeStore(storeKind, storePath)
			if err != nil {
				return err
			}

			daemonServer, err := daemon.NewServer(daemon.ServerConfig{
				Store: store,
			})
			if err != nil {
				return fmt.Errorf("creating daemon server: %w", err)
			}

			configPath, found, err := daemon.DiscoverToolConfigPath(explicitConfigPath)
			if err != nil {
				return err
			}
			if found {
				registered, err := daemon.RegisterToolsFromConfig(cmd.Context(), daemonServer.Service(), configPath)
				if err != nil {
					return fmt.Errorf("loading startup tool declarations: %w", err)
				}
				if err := daemonServer.SyncRegistry(cmd.Context()); err != nil {
					return fmt.Errorf("syncing registry after startup config load: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Loaded %d tool declaration(s) from %s\n", len(registered), configPath)
			}

			addr := fmt.Sprintf("%s:%d", host, port)
			handler := withCORS(daemonServer.Handler(), corsOrigin)
			server := &http.Server{
				Addr:              addr,
				Handler:           handler,
				ReadHeaderTimeout: 5 * time.Second,
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Daemon listening on http://%s\n", addr)
			err = server.ListenAndServe()
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		},
	}

	cmd.Flags().IntP("port", "p", 8080, "Listen port")
	cmd.Flags().String("host", "0.0.0.0", "Listen host")
	cmd.Flags().String("cors-origin", "*", "Allowed CORS origin")
	cmd.Flags().String("store", "memory", "Workflow store backend: memory | file")
	cmd.Flags().String("store-path", "", "File store directory (only for --store file)")
	cmd.Flags().String("config", "", "Path to petalflow.yaml tool config")
	cmd.Flags().StringArray("provider-key", nil, "Set provider API key (repeatable)")

	return cmd
}

func resolveServeStore(kind, storePath string) (tool.Store, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "memory":
		return daemon.NewMemoryToolStore(), nil
	case "file":
		if strings.TrimSpace(storePath) == "" {
			return tool.NewDefaultFileStore()
		}
		return tool.NewFileStore(filepath.Clean(storePath)), nil
	default:
		return nil, fmt.Errorf(`invalid --store %q (use "memory" or "file")`, kind)
	}
}

func withCORS(next http.Handler, allowedOrigin string) http.Handler {
	origin := strings.TrimSpace(allowedOrigin)
	if origin == "" {
		origin = "*"
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
