// analytics-mcp serves GA4, Search Console and Google Ads over two surfaces:
//
//   - MCP (Streamable HTTP) for agents: ga4_report, gsc_query, ads_search,
//     ads_keyword_ideas, ads_set_campaign_status, ads_push_negative_keywords.
//   - A flat JSON API for dashboards: POST /query and POST /action, shaped so a
//     client can keep the request/response contract it had with a third-party
//     data aggregator.
//
// Both surfaces use the same OAuth stack (Google login, token store, DCR/CIMD)
// as the self-hosted GTM MCP server this service was derived from.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"analytics-mcp/api"
	"analytics-mcp/auth"
	"analytics-mcp/config"
	"analytics-mcp/dashboard"
	"analytics-mcp/middleware"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"
)

//go:embed llms.txt
var llmsTxt string

const serverName = "analytics-mcp"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	serverVersion, err := parseServerVersion(serverMetadata)
	if err != nil {
		logger.Error("invalid embedded server metadata", "error", err)
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	if cfg.LogLevel == "debug" {
		logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
		slog.SetDefault(logger)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Version: serverVersion,
	}, nil)
	server.AddReceivingMiddleware(middleware.NewLoggingMiddleware(logger))
	registerTools(server)
	logger.Info("registered tools", "tools", 16, "connectors", api.SupportedConnectors)

	switch transport := transportFromArgs(os.Args[1:], cfg.Transport); transport {
	case "stdio":
		if err := runStdio(server, cfg, logger); err != nil {
			logger.Error("stdio server error", "error", err)
			os.Exit(1)
		}
		return
	case "http":
	default:
		logger.Error("invalid MCP_TRANSPORT", "transport", transport, "allowed", "http, stdio")
		os.Exit(1)
	}

	mcpHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: cfg.StatelessHTTP})

	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "healthy",
			"service": serverName,
			"version": serverVersion,
		})
	})

	mux.HandleFunc("GET /llms.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(llmsTxt))
	})

	var urlResolver *auth.URLResolver
	if len(cfg.AllowedHosts) > 0 {
		urlResolver = auth.NewURLResolver(cfg.BaseURL, cfg.AllowedHosts)
		logger.Info("dynamic URL resolution enabled", "allowed_hosts", cfg.AllowedHosts)
	}

	mux.HandleFunc("GET /.well-known/oauth-protected-resource",
		auth.ProtectedResourceMetadataHandler(cfg.BaseURL, cfg.BaseURL, urlResolver))
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", auth.MetadataHandler(cfg.BaseURL, urlResolver))

	var saTokenSource oauth2.TokenSource
	if cfg.ServiceAccountAPIKey != "" {
		var saErr error
		saTokenSource, saErr = auth.NewServiceAccountTokenSource(context.Background(), cfg.ServiceAccountKeyJSON)
		if saErr != nil {
			logger.Error("s2s_mode_failed", "error", saErr,
				"hint", "set GOOGLE_SERVICE_ACCOUNT_KEY_JSON or GOOGLE_APPLICATION_CREDENTIALS")
			os.Exit(1)
		}
		logger.Info("s2s_mode_enabled")
	}

	var authServer *auth.Server
	var tokenStore auth.TokenStore
	oauthConfigured := cfg.ValidateAuth() == nil

	oauthLimiter := middleware.NewRateLimiter(10, 20, cfg.TrustProxy)
	registerLimiter := middleware.NewRateLimiter(2, 5, cfg.TrustProxy)

	// helper to answer "OAuth is not configured" for clients that discover it.
	oauthNotConfigured := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error":             "server_error",
			"error_description": "OAuth is not configured on this server.",
		})
	}

	if oauthConfigured {
		if cfg.TokenStorePath != "" {
			fileStore, err := auth.NewFileTokenStore(cfg.TokenStorePath, logger)
			if err != nil {
				logger.Error("failed to initialize file token store", "error", err, "path", cfg.TokenStorePath)
				os.Exit(1)
			}
			tokenStore = fileStore
			logger.Info("token persistence enabled", "path", cfg.TokenStorePath)
		} else {
			tokenStore = auth.NewMemoryTokenStore()
		}
		googleProvider := auth.NewGoogleProvider(
			cfg.GoogleClientID,
			cfg.GoogleClientSecret,
			cfg.BaseURL+"/oauth/callback",
		)
		authServer = auth.NewServer(cfg.BaseURL, googleProvider, tokenStore, logger, cfg.AccessTokenTTL)
		if urlResolver != nil {
			authServer.SetURLResolver(urlResolver)
		}

		mux.HandleFunc("GET /authorize", oauthLimiter.MiddlewareFunc(authServer.AuthorizeHandler))
		mux.HandleFunc("GET /oauth/callback", oauthLimiter.MiddlewareFunc(authServer.CallbackHandler))
		mux.HandleFunc("POST /token", oauthLimiter.MiddlewareFunc(middleware.MaxBytesMiddleware(1<<20, authServer.TokenHandler)))
		mux.HandleFunc("POST /register", registerLimiter.MiddlewareFunc(middleware.MaxBytesMiddleware(1<<20, authServer.RegistrationHandler)))

		authMiddleware := auth.Middleware(tokenStore, googleProvider, logger, cfg.BaseURL, cfg.AccessTokenTTL, urlResolver, saTokenSource, cfg.ServiceAccountAPIKey, cfg.AutoRefreshMaxAge)

		// Both surfaces need the Google token, so both sit behind the middleware.
		mux.Handle("POST /query", authMiddleware(api.QueryHandler(logger)))
		mux.Handle("POST /action", authMiddleware(api.ActionHandler(logger)))
		mux.Handle("/", authMiddleware(maxBytesHandler(5<<20, mcpHandler)))

		logger.Info("OAuth configured",
			"authorize_endpoint", cfg.BaseURL+"/authorize",
			"token_endpoint", cfg.BaseURL+"/token",
			"callback_endpoint", cfg.BaseURL+"/oauth/callback",
			"register_endpoint", cfg.BaseURL+"/register",
		)
	} else {
		logger.Warn("No OAuth configured (GOOGLE_CLIENT_ID/SECRET missing)")
		mux.HandleFunc("GET /authorize", oauthNotConfigured)
		mux.HandleFunc("GET /oauth/callback", oauthNotConfigured)
		mux.HandleFunc("POST /token", oauthNotConfigured)
		mux.HandleFunc("POST /register", oauthNotConfigured)
		mux.Handle("POST /query", api.QueryHandler(logger))
		mux.Handle("POST /action", api.ActionHandler(logger))
		mux.Handle("/", maxBytesHandler(5<<20, mcpHandler))
	}

	if cfg.DashboardEnabled {
		mux.Handle("/dashboard/", dashboard.NewHandler(dashboard.Status{
			Endpoint:        cfg.BaseURL,
			Version:         serverVersion,
			OAuthConfigured: oauthConfigured,
		}))
	}

	addr := fmt.Sprintf(":%d", cfg.Port)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      0, // disabled for SSE streams
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("starting analytics MCP server", "port", cfg.Port, "base_url", cfg.BaseURL)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down server")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", "error", err)
	}
	logger.Info("server stopped")
}

// maxBytesHandler wraps an http.Handler with a request body size limit.
func maxBytesHandler(maxBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}
