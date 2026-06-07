package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/Dirard/mcp-file-tools/filetoolsserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is set at build time via ldflags
var version = "dev"

func main() {
	// Set version from build
	filetoolsserver.Version = version

	versionFlag := flag.Bool("version", false, "print version and exit")
	versionShortFlag := flag.Bool("v", false, "print version and exit")
	httpAddr := flag.String("http", os.Getenv("MCP_HTTP_ADDR"), "serve streamable HTTP on this address, for example 127.0.0.1:8787")
	logFile := flag.String("log-file", os.Getenv("MCP_LOG_FILE"), "append HTTP server logs to this file")
	flag.Parse()

	if *versionFlag || *versionShortFlag {
		fmt.Println(version)
		return
	}

	var logger *slog.Logger
	if *httpAddr != "" {
		logWriter, closeLog, err := openLogWriter(*logFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Log file error: %v\n", err)
			os.Exit(1)
		}
		defer closeLog()
		logger = slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	// Create MCP server.
	// Pass nil for logger in stdio mode to keep the transport quiet.
	// Pass nil for config to load MCP_* runtime settings from environment variables.
	server := filetoolsserver.NewServer(logger, nil)

	if *httpAddr != "" {
		runHTTPServer(*httpAddr, server, logger)
		return
	}

	// Run server on stdio transport
	ctx := context.Background()
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

func openLogWriter(path string) (io.Writer, func(), error) {
	if path == "" {
		return os.Stderr, func() {}, nil
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, nil, err
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, nil, err
	}
	return file, func() { _ = file.Close() }, nil
}

func runHTTPServer(addr string, server *mcp.Server, logger *slog.Logger) {
	streamableHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, nil)

	mux := http.NewServeMux()
	mux.Handle("/mcp", streamableHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	logger.Info("http_server_start", "addr", addr, "endpoint", "/mcp")
	if err := http.ListenAndServe(addr, mux); err != nil {
		logger.Error("http_server_error", "addr", addr, "error", err)
		os.Exit(1)
	}
}
