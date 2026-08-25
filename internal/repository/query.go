package repository

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/state"
)

type Query struct {
	Text     string
	Path     string
	Kind     string
	Limit    int
	Exact    bool
	FullText bool
}

type QueryResult struct {
	Candidates []state.RepositoryCandidate
	Relations  []state.RepositoryRelation
}

// Query waits for the first scan, prefers exact path/symbol matches, and only
// widens to BM25 or semantic lookup when lexical evidence is weak.
func (s *Service) Query(ctx context.Context, input Query) (result QueryResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("repository query panicked: %v", r)
		}
	}()
	if err := s.Wait(ctx); err != nil {
		return QueryResult{}, err
	}
	s.mu.Lock()
	dirty := s.dirty
	s.mu.Unlock()
	if dirty {
		if _, err := s.Scan(ctx); err != nil {
			return QueryResult{}, err
		}
	}
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return QueryResult{}, nil
	}
	clearIdentifier := isIdentifier(text)
	candidates, err := s.store.RepositoryCandidates(ctx, state.RepositoryLookup{Query: text, ScopePath: input.Path, Kind: input.Kind, Limit: input.Limit, ExactOnly: input.Exact, Prefix: clearIdentifier, FullText: input.FullText || !clearIdentifier})
	if err != nil {
		return QueryResult{}, err
	}
	if input.Path != "" {
		scope, err := s.relativePath(input.Path)
		if err != nil {
			return QueryResult{}, err
		}
		candidates = filterScope(candidates, scope)
	}
	result = QueryResult{Candidates: candidates}
	for _, candidate := range candidates {
		if candidate.SymbolID == "" {
			continue
		}
		relations, err := s.store.RepositoryNeighbors(ctx, candidate.SymbolID, state.RelationBoth, 20)
		if err != nil {
			return QueryResult{}, err
		}
		result.Relations = append(result.Relations, relations...)
		ids := make([]string, 0, len(relations))
		for _, relation := range relations {
			if relation.SourceSymbolID != "" && relation.SourceSymbolID != candidate.SymbolID {
				ids = append(ids, relation.SourceSymbolID)
			}
			if relation.TargetSymbolID != "" && relation.TargetSymbolID != candidate.SymbolID {
				ids = append(ids, relation.TargetSymbolID)
			}
		}
		neighbors, err := s.store.RepositorySymbolCandidatesByID(ctx, unique(ids))
		if err != nil {
			return QueryResult{}, err
		}
		for index := range neighbors {
			neighbors[index].GraphDistance = 1
		}
		result.Candidates = appendUnique(result.Candidates, neighbors, input.Limit)
	}
	if len(result.Candidates) == 0 || (!clearIdentifier && len(result.Candidates) < max(3, input.Limit/3)) {
		semantic, err := s.semantic(ctx, text, input.Limit)
		if err == nil {
			result.Candidates = appendUnique(result.Candidates, semantic, input.Limit)
		}
	}
	return result, nil
}

func filterScope(candidates []state.RepositoryCandidate, scope string) []state.RepositoryCandidate {
	if scope == "." || scope == "" {
		return candidates
	}
	prefix := strings.TrimSuffix(scope, "/") + "/"
	result := candidates[:0]
	for _, candidate := range candidates {
		if candidate.Path == scope || strings.HasPrefix(candidate.Path, prefix) {
			result = append(result, candidate)
		}
	}
	return result
}

func (s *Service) SetSemantic(ctx context.Context, enabled bool) error {
	if err := s.store.SetRepositorySemanticSettings(ctx, state.SemanticSettings{Enabled: enabled}); err != nil {
		return err
	}
	if enabled {
		s.scheduleEmbeddings()
	}
	return nil
}

func (s *Service) SemanticStatus(ctx context.Context) (state.SemanticSettings, bool, error) {
	settings, err := s.store.RepositorySemanticSettings(ctx)
	return settings, s.embeddingProvider() != nil, err
}

func (s *Service) scheduleEmbeddings() {
	provider := s.embeddingProvider()
	if provider == nil || provider.Model() == "" {
		return
	}
	go func() {
		// ponytail: this is an O(N) bounded in-process pass; use a SQLite vector
		// extension or ANN index only after repository benchmarks require it.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		chunks, err := s.store.RepositoryCurrentChunks(ctx)
		if err != nil {
			return
		}
		existing, err := s.store.RepositoryEmbeddings(ctx, provider.Model())
		if err != nil {
			return
		}
		known := map[string]string{}
		for _, embedding := range existing {
			known[embedding.ChunkID] = embedding.SourceHash
		}
		for start := 0; start < len(chunks); start += 20 {
			end := min(start+20, len(chunks))
			batch := chunks[start:end]
			input, pending := make([]string, 0, len(batch)), make([]state.RepositoryCandidate, 0, len(batch))
			for _, chunk := range batch {
				if known[chunk.ID] == chunk.Hash {
					continue
				}
				input, pending = append(input, chunk.Content), append(pending, chunk)
			}
			if len(input) == 0 {
				continue
			}
			vectors, err := provider.Embed(ctx, input)
			if err != nil || len(vectors) != len(pending) {
				return
			}
			writes := make([]state.RepositoryEmbeddingInput, 0, len(vectors))
			for index, vector := range vectors {
				writes = append(writes, state.RepositoryEmbeddingInput{ChunkID: pending[index].ID, SourceHash: pending[index].Hash, Model: provider.Model(), Vector: encodeVector(vector), Dimensions: len(vector)})
			}
			if s.store.PutRepositoryEmbeddings(ctx, writes) != nil {
				return
			}
		}
	}()
}

func (s *Service) semantic(ctx context.Context, text string, limit int) ([]state.RepositoryCandidate, error) {
	settings, err := s.store.RepositorySemanticSettings(ctx)
	provider := s.embeddingProvider()
	if err != nil || !settings.Enabled || provider == nil || provider.Model() == "" {
		return nil, err
	}
	vectors, err := provider.Embed(ctx, []string{text})
	if err != nil || len(vectors) != 1 {
		return nil, err
	}
	embeddings, err := s.store.RepositoryEmbeddings(ctx, provider.Model())
	if err != nil {
		return nil, err
	}
	type scored struct {
		id    string
		score float64
	}
	scores := make([]scored, 0, len(embeddings))
	for _, embedding := range embeddings {
		vector, ok := decodeVector(embedding.Vector, embedding.Dimensions)
		if !ok {
			continue
		}
		if score := cosine(vectors[0], vector); score > 0 {
			scores = append(scores, scored{id: embedding.ChunkID, score: score})
		}
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].score > scores[j].score })
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if len(scores) > limit {
		scores = scores[:limit]
	}
	ids := make([]string, len(scores))
	byID := make(map[string]float64, len(scores))
	for index, score := range scores {
		ids[index], byID[score.id] = score.id, score.score
	}
	candidates, err := s.store.RepositoryCandidatesByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	for index := range candidates {
		candidates[index].SemanticSimilarity = byID[candidates[index].ID]
	}
	return candidates, nil
}

func isIdentifier(value string) bool {
	for _, rune := range value {
		if !(rune == '_' || rune == '.' || rune >= 'a' && rune <= 'z' || rune >= 'A' && rune <= 'Z' || rune >= '0' && rune <= '9') {
			return false
		}
	}
	return value != ""
}

func appendUnique(current, extra []state.RepositoryCandidate, limit int) []state.RepositoryCandidate {
	seen := make(map[string]bool, len(current)+len(extra))
	for _, candidate := range current {
		seen[candidate.ID] = true
	}
	for _, candidate := range extra {
		if !seen[candidate.ID] {
			seen[candidate.ID] = true
			current = append(current, candidate)
		}
		if limit > 0 && len(current) >= limit {
			break
		}
	}
	return current
}

func unique(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
