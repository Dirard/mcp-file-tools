package main

import (
	"context"
	"os"

	"github.com/Dirard/mcp-file-tools/internal/app"
)

var version = "dev"

func main() {
	app.Version = version
	os.Exit(app.Run(context.Background(), os.Args[1:], app.OSEnvironment{}, app.IO{
		In:  os.Stdin,
		Out: os.Stdout,
		Err: os.Stderr,
	}))
}
