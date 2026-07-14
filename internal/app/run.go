package app

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"

	"github.com/Dirard/mcp-file-tools/internal/codeparse"
	"github.com/Dirard/mcp-file-tools/internal/config"
	"github.com/Dirard/mcp-file-tools/internal/cursor"
	"github.com/Dirard/mcp-file-tools/internal/cwd"
	"github.com/Dirard/mcp-file-tools/internal/mcpstdio"
	"github.com/Dirard/mcp-file-tools/internal/navigation"
	workruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
	"github.com/Dirard/mcp-file-tools/internal/scanner"
)

const fatalLine = "mcp-file-tools-v2: fatal\n"

var (
	// Version is replaced at link time by release builds.
	Version = "dev"

	errAssembly = errors.New("app: assembly failed")
)

// Environment is the closed startup environment boundary.
type Environment interface {
	Lookup(string) (string, bool)
}

// OSEnvironment reads the process environment.
type OSEnvironment struct{}

func (OSEnvironment) Lookup(name string) (string, bool) {
	return os.LookupEnv(name)
}

// IO is the strict stdio process boundary.
type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

type fatalOutput struct {
	once   sync.Once
	writer io.Writer
}

func (output *fatalOutput) write() {
	if output == nil || output.writer == nil {
		return
	}
	output.once.Do(func() {
		func() {
			defer func() { _ = recover() }()
			_, _ = io.WriteString(output.writer, fatalLine)
		}()
	})
}

type connectionFactory struct {
	config  config.Runtime
	service *navigation.Service
}

func (factory *connectionFactory) NewConnection() (mcpstdio.CallExecutor, error) {
	if factory == nil || factory.service == nil {
		return nil, errAssembly
	}
	cursors, err := cursor.New(factory.config)
	if err != nil {
		return nil, errAssembly
	}
	dispatcher, err := navigation.NewDispatcher(&navigation.Connection{
		Service: factory.service,
		Cursors: cursors,
	})
	if err != nil {
		_ = cursors.Close()
		return nil, errAssembly
	}
	return dispatcher, nil
}

// Run validates startup once and owns the single stdio connection.
func Run(ctx context.Context, args []string, environment Environment, streams IO) (exitCode int) {
	fatal := &fatalOutput{writer: streams.Err}
	defer func() {
		if recover() != nil {
			fatal.write()
			exitCode = 1
		}
	}()

	if len(args) == 1 && (args[0] == "-version" || args[0] == "-v") {
		if streams.Out == nil {
			fatal.write()
			return 1
		}
		if _, err := io.WriteString(streams.Out, Version+"\n"); err != nil {
			return 1
		}
		return 0
	}
	if len(args) != 0 || ctx == nil || environment == nil || streams.In == nil || streams.Out == nil || streams.Err == nil {
		fatal.write()
		return 1
	}

	runtimeConfig, err := config.LoadRuntime(environment.Lookup)
	if err != nil {
		fatal.write()
		return 1
	}

	scanLimiter := workruntime.NewSubLimiter(runtimeConfig.ScanMaxCalls)
	parseLimiter := workruntime.NewSubLimiter(runtimeConfig.ParseMaxCalls)
	parserCache := codeparse.NewCache(runtimeConfig.ParserCacheMaxEntries, runtimeConfig.ParserCacheMaxBytes)
	parser := codeparse.NewService(runtimeConfig, parserCache, parseLimiter)
	if scanLimiter == nil || parseLimiter == nil || parserCache == nil || parser == nil {
		fatal.write()
		return 1
	}

	cwdRegistry := cwd.New(runtimeConfig, nil)
	defer func() { _ = cwdRegistry.Close() }()
	service := &navigation.Service{
		Config:      runtimeConfig,
		CWD:         cwdRegistry,
		Parser:      parser,
		ScanLimiter: scanLimiter,
		Scanner:     scanner.NewService(scanLimiter),
	}
	factory := &connectionFactory{config: runtimeConfig, service: service}
	server := mcpstdio.NewServer(workruntime.Limits{
		MaxConcurrent: runtimeConfig.CallMaxConcurrent,
		QueueMax:      runtimeConfig.CallQueueMax,
		QueueTimeout:  runtimeConfig.CallQueueTimeout,
	}, factory)
	server.Version = Version

	err = server.Serve(ctx, streams.In, streams.Out)
	if err == nil {
		return 0
	}
	if errors.Is(err, errAssembly) || errors.Is(err, workruntime.ErrInternalFatal) {
		fatal.write()
	}
	return 1
}
