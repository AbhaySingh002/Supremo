package search

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/repository"
	"github.com/AbhaySingh002/supremo/internal/state"
)

func indexedQuery(ctx context.Context, query repository.Query) (repository.QueryResult, *repository.Service, bool, error) {
	index := repository.FromContext(ctx)
	if index == nil {
		return repository.QueryResult{}, nil, false, nil
	}
	result, err := index.Query(ctx, query)
	return result, index, true, err
}

func candidateInDirectory(index *repository.Service, candidate state.RepositoryCandidate, directory string) bool {
	relative, err := filepath.Rel(index.Root(), directory)
	if err != nil {
		return false
	}
	relative = filepath.ToSlash(relative)
	return relative == "." || candidate.Path == relative || strings.HasPrefix(candidate.Path, strings.TrimSuffix(relative, "/")+"/")
}

func indexedPath(index *repository.Service, candidate state.RepositoryCandidate) string {
	return filepath.Join(index.Root(), filepath.FromSlash(candidate.Path))
}
