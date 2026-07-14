package catalog

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestV2FoundationDependencyIsolation(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate dependency test")
	}
	moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	packages := []string{"./internal/api", "./internal/config", "./internal/catalog"}
	args := append([]string{"list", "-deps", "-f", "{{.ImportPath}}"}, packages...)
	command := exec.Command("go", args...)
	command.Dir = moduleRoot
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list v2 foundation dependencies: %v", err)
	}

	forbidden := []string{
		"github.com/modelcontextprotocol/go-sdk",
		"github.com/google/jsonschema-go",
		"modernc.org/sqlite",
		"github.com/colbymchenry/codegraph",
	}
	for _, dependency := range strings.Fields(string(output)) {
		for _, prefix := range forbidden {
			if dependency == prefix || strings.HasPrefix(dependency, prefix+"/") {
				t.Errorf("v2 foundation depends on forbidden package %q", dependency)
			}
		}
	}
}
