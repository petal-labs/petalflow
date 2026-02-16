package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	otelapi "go.opentelemetry.io/otel"

	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/daemon"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/llmprovider"
	petalotel "github.com/petal-labs/petalflow/otel"
	"github.com/petal-labs/petalflow/server"
	"github.com/petal-labs/petalflow/tool"
)

// NewServeCmd creates the "serve" subcommand.
func NewServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the daemon HTTP server",
		RunE:  runServe,
	}

	cmd.Flags().IntP("port", "p", 8080, "Listen port")
	cmd.Flags().String("host", "0.0.0.0", "Listen host")
	cmd.Flags().String("cors-origin", "*", "Allowed CORS origin")
	cmd.Flags().String("sqlite-path", "", "Path to SQLite database (default: ~/.petalflow/petalflow.db)")
	cmd.Flags().String("config", "", "Path to petalflow.yaml tool config")
	cmd.Flags().StringArray("provider-key", nil, "Set provider API key (repeatable)")
	cmd.Flags().String("tls-cert", "", "TLS certificate file")
	cmd.Flags().String("tls-key", "", "TLS key file")
	cmd.Flags().Duration("read-timeout", 30*time.Second, "HTTP read timeout")
	cmd.Flags().Duration("write-timeout", 60*time.Second, "HTTP write timeout")
	cmd.Flags().Int64("max-body", 1<<20, "Max request body size in bytes")
	cmd.Flags().Duration("workflow-schedule-poll", 5*time.Second, "Workflow schedule poll interval")

	return cmd
}

func runServe(cmd *cobra.Command, _ []string) error {
	host, _ := cmd.Flags().GetString("host")
	port, _ := cmd.Flags().GetInt("port")
	corsOrigin, _ := cmd.Flags().GetString("cors-origin")
	readTimeout, _ := cmd.Flags().GetDuration("read-timeout")
	writeTimeout, _ := cmd.Flags().GetDuration("write-timeout")
	maxBody, _ := cmd.Flags().GetInt64("max-body")
	workflowSchedulePoll, _ := cmd.Flags().GetDuration("workflow-schedule-poll")
	tlsCert, _ := cmd.Flags().GetString("tls-cert")
	tlsKey, _ := cmd.Flags().GetString("tls-key")
	explicitConfigPath, _ := cmd.Flags().GetString("config")

	sqliteDSN, sqliteScope, err := resolveServeSQLiteDSN(cmd)
	if err != nil {
		return err
	}

	// --- Daemon tool server (Phase 3) ---
	toolStore, err := tool.NewSQLiteStore(tool.SQLiteStoreConfig{
		DSN:   sqliteDSN,
		Scope: sqliteScope,
	})
	if err != nil {
		return fmt.Errorf("opening sqlite tool store: %w", err)
	}
	defer func() {
		_ = toolStore.Close()
	}()

	daemonServer, err := daemon.NewServer(daemon.ServerConfig{
		Store: toolStore,
	})
	if err != nil {
		return fmt.Errorf("creating daemon server: %w", err)
	}

	toolObserver, err := petalotel.NewToolObserver(
		otelapi.GetMeterProvider().Meter("petalflow/tool"),
		otelapi.GetTracerProvider().Tracer("petalflow/tool"),
	)
	if err != nil {
		return fmt.Errorf("initializing tool observability: %w", err)
	}
	tool.SetObserver(toolObserver)
	defer tool.SetObserver(nil)

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

	healthScheduler, err := tool.NewHealthScheduler(tool.HealthSchedulerConfig{
		Service: daemonServer.Service(),
	})
	if err != nil {
		return fmt.Errorf("creating health scheduler: %w", err)
	}
	if err := healthScheduler.Start(cmd.Context()); err != nil {
		return fmt.Errorf("starting health scheduler: %w", err)
	}
	defer func() {
		_ = healthScheduler.Stop(context.Background())
	}()

	// --- Workflow API server ---
	providerFlags, _ := cmd.Flags().GetStringArray("provider-key")
	flagMap, err := hydrate.ParseProviderFlags(providerFlags)
	if err != nil {
		return exitError(exitProvider, "invalid provider flag: %v", err)
	}
	providers, err := hydrate.ResolveProviders(flagMap)
	if err != nil {
		return exitError(exitProvider, "resolving providers: %v", err)
	}

	eb := bus.NewMemBus(bus.MemBusConfig{})
	es, err := bus.NewSQLiteEventStore(bus.SQLiteStoreConfig{DSN: sqliteDSN})
	if err != nil {
		return fmt.Errorf("opening sqlite event store: %w", err)
	}
	defer func() {
		_ = es.Close()
	}()

	workflowStore, err := server.NewSQLiteStore(server.SQLiteStoreConfig{DSN: sqliteDSN})
	if err != nil {
		return fmt.Errorf("opening sqlite workflow store: %w", err)
	}
	defer func() {
		_ = workflowStore.Close()
	}()
	logger := slog.Default()

	workflowServer := server.NewServer(server.ServerConfig{
		Store:         workflowStore,
		ScheduleStore: workflowStore,
		ToolStore:     toolStore,
		Providers:     providers,
		ClientFactory: func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
			return llmprovider.NewClient(name, cfg)
		},
		Bus:        eb,
		EventStore: es,
		CORSOrigin: corsOrigin,
		MaxBody:    maxBody,
		Logger:     logger,
	})

	workflowScheduler, err := server.NewWorkflowScheduler(server.WorkflowSchedulerConfig{
		Runner:       workflowServer,
		Store:        workflowStore,
		PollInterval: workflowSchedulePoll,
		Logger:       logger,
	})
	if err != nil {
		return fmt.Errorf("creating workflow scheduler: %w", err)
	}
	if err := workflowScheduler.Start(cmd.Context()); err != nil {
		return fmt.Errorf("starting workflow scheduler: %w", err)
	}
	defer func() {
		_ = workflowScheduler.Stop(context.Background())
	}()

	// Compose both handlers on one mux.
	// Workflow routes: /health, /api/workflows/*, /api/runs/*, /api/node-types
	// Daemon routes: /api/tools/*
	mux := http.NewServeMux()
	workflowServer.RegisterRoutes(mux)
	daemonHandler := daemonServer.Handler()
	mux.Handle("/api/tools/", daemonHandler)
	mux.Handle("/api/tools", daemonHandler)

	handler := withCORS(mux, corsOrigin)
	handler = maxBodyMiddleware(handler, maxBody)

	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}

	// Signal handling
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(cmd.OutOrStdout(), "PetalFlow daemon listening on %s\n", addr)
		if tlsCert != "" && tlsKey != "" {
			errCh <- httpServer.ListenAndServeTLS(tlsCert, tlsKey)
		} else {
			errCh <- httpServer.ListenAndServe()
		}
	}()

	select {
	case <-ctx.Done():
		fmt.Fprintln(cmd.OutOrStdout(), "Shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return exitError(exitRuntime, "shutdown error: %v", err)
		}
		_ = eb.Close()
		return nil
	case err := <-errCh:
		_ = eb.Close()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return exitError(exitRuntime, "server error: %v", err)
		}
		return nil
	}
}

func resolveServeSQLiteDSN(cmd *cobra.Command) (string, string, error) {
	sqlitePath, _ := cmd.Flags().GetString("sqlite-path")
	dsn := strings.TrimSpace(sqlitePath)
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("PETALFLOW_SQLITE_PATH"))
	}
	if dsn == "" {
		// Backward-compatible fallback for existing automation/env usage.
		dsn = strings.TrimSpace(os.Getenv("PETALFLOW_TOOLS_STORE_PATH"))
	}
	if dsn == "" {
		defaultPath, err := tool.DefaultSQLitePath()
		if err != nil {
			return "", "", fmt.Errorf("resolving default sqlite path: %w", err)
		}
		dsn = defaultPath
	}

	scope := dsn
	if !strings.HasPrefix(strings.ToLower(dsn), "file:") {
		clean := filepath.Clean(dsn)
		dsn = clean
		scope = clean
	}
	return dsn, scope, nil
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

func maxBodyMiddleware(next http.Handler, maxBody int64) http.Handler {
	if maxBody <= 0 {
		maxBody = 1 << 20
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		next.ServeHTTP(w, r)
	})
}
