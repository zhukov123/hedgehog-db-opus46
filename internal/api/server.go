package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/hedgehog-db/hedgehog/internal/table"
)

// Server is the HTTP API server.
type Server struct {
	httpServer   *http.Server
	router       *Router
	handlers     *Handlers
	tableManager *table.TableManager
}

// NewServer creates a new API server. Coordinator may be nil for standalone mode (no replication).
func NewServer(bindAddr string, tm *table.TableManager, coordinator ItemCoordinator) *Server {
	router := NewRouter()

	// Apply middleware (outermost runs first)
	router.Use(RecoveryMiddleware)
	router.Use(CORSMiddleware)
	router.Use(TraceIDMiddleware)
	router.Use(TracingMiddleware)
	router.Use(MetricsMiddleware)
	router.Use(LoggingMiddleware)

	handlers := NewHandlers(tm, coordinator)

	// Register routes
	router.POST("/api/v1/tables", handlers.CreateTable)
	router.GET("/api/v1/tables", handlers.ListTables)
	router.DELETE("/api/v1/tables/{name}", handlers.DeleteTable)

	router.GET("/api/v1/tables/{name}/count", handlers.TableCount)
	router.GET("/api/v1/tables/{name}/items", handlers.ScanItems)
	router.GET("/api/v1/tables/{name}/items/{key}", handlers.GetItem)
	router.PUT("/api/v1/tables/{name}/items/{key}", handlers.PutItem)
	router.DELETE("/api/v1/tables/{name}/items/{key}", handlers.DeleteItem)

	// Health check
	router.GET("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	return &Server{
		httpServer: &http.Server{
			Addr:         bindAddr,
			Handler:      router,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
		router:       router,
		handlers:     handlers,
		tableManager: tm,
	}
}

// Router returns the router for registering additional routes.
func (s *Server) Router() *Router {
	return s.router
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	log.Printf("Starting HedgehogDB server on %s", s.httpServer.Addr)
	err := s.httpServer.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	log.Println("Shutting down server...")
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return s.tableManager.Close()
}
