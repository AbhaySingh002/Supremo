package search

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

func walkSourceFiles(ctx context.Context, root, language string, visit func(path string, lines []string) error) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return nil
		}
		if info.IsDir() {
			if tools.IsHidden(info.Name()) && path != root {
				return filepath.SkipDir
			}
			depth, err := tools.SearchDepth(root, path)
			if err != nil {
				return err
			}
			if depth > tools.MaxSearchDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if tools.ShouldSkipFile(path) || !matchesLanguage(path, language) {
			return nil
		}
		depth, err := tools.SearchDepth(root, path)
		if err != nil || depth > tools.MaxSearchDepth {
			return err
		}
		content, err := tools.ReadSearchFile(path)
		if err != nil {
			return nil
		}
		return visit(path, strings.Split(string(content), "\n"))
	})
}
