package ui

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProductionUIDependsOnlyOnFrontendContracts(t *testing.T) {
	forbidden := []string{
		"/internal/agent",
		"/internal/app",
		"/internal/backend",
		"/internal/context",
		"/internal/interaction",
		"/internal/providers",
		"/internal/repository",
		"/internal/sessionlog",
		"/internal/state",
		"/internal/tools",
	}
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return walkErr
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			for _, blocked := range forbidden {
				if strings.Contains(importPath, blocked) {
					t.Errorf("%s imports backend implementation package %s", path, importPath)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
