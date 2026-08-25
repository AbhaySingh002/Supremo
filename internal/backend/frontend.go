package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/repository"
	"github.com/AbhaySingh002/supremo/internal/tools"
	toolgit "github.com/AbhaySingh002/supremo/internal/tools/git"
)

const artifactPreviewLimit = 64 << 10

func (s *Service) ClearSession(ctx context.Context, request api.SessionRequest) (api.SessionSnapshot, error) {
	if strings.TrimSpace(request.SessionID) == "" {
		return api.SessionSnapshot{}, apiError(api.CodeInvalidArgument, "session_id is required", false)
	}
	if err := s.runtimes.ClearMemory(ctx, request.SessionID); err != nil {
		return api.SessionSnapshot{}, err
	}
	return s.GetSession(ctx, request.SessionID)
}

func (s *Service) ResetSession(ctx context.Context, request api.SessionRequest) (api.SessionSnapshot, error) {
	if _, err := s.ClearSession(ctx, request); err != nil {
		return api.SessionSnapshot{}, err
	}
	if err := tools.ClearCheckpoints(s.workspace, request.SessionID); err != nil {
		return api.SessionSnapshot{}, err
	}
	session, err := agent.LoadSession(s.workspace, request.SessionID)
	if err != nil {
		return api.SessionSnapshot{}, mapStateError(err, "session")
	}
	session.ActiveTaskID = ""
	session.DryRun = false
	session.ApprovalMode = tools.ApprovalBatman
	if err := session.Save(s.workspace); err != nil {
		return api.SessionSnapshot{}, err
	}
	return s.GetSession(ctx, request.SessionID)
}

func (s *Service) ListCheckpoints(_ context.Context, request api.SessionRequest) ([]api.Checkpoint, error) {
	items, err := s.runtimes.Checkpoints(s.workspace, request.SessionID)
	if err != nil {
		return nil, err
	}
	result := make([]api.Checkpoint, 0, len(items))
	for _, item := range items {
		result = append(result, checkpointDTO(item))
	}
	return result, nil
}

func (s *Service) RewindSession(ctx context.Context, request api.RewindRequest) (api.RewindResult, error) {
	result, err := s.runtimes.Rewind(ctx, s.workspace, request.SessionID, request.Checkpoint, request.Force)
	if err != nil {
		return api.RewindResult{}, err
	}
	value := api.RewindResult{Restored: result.Restored, Partial: result.Partial}
	for _, warning := range result.Warnings {
		value.Warnings = append(value.Warnings, api.CheckpointWarning{Path: warning.Path, Reason: warning.Reason})
	}
	if result.Backup != nil {
		backup := checkpointDTO(*result.Backup)
		value.Backup = &backup
	}
	return value, nil
}

func checkpointDTO(item tools.CheckpointSummary) api.Checkpoint {
	value := api.Checkpoint{ID: item.ID, CreatedAt: item.CreatedAt, Action: item.Action, Files: item.Files, Partial: item.Partial}
	for _, warning := range item.Warnings {
		value.Warnings = append(value.Warnings, api.CheckpointWarning{Path: warning.Path, Reason: warning.Reason})
	}
	return value
}

func (s *Service) AnswerSideQuestion(ctx context.Context, request api.SideQuestionRequest) (api.SideQuestionResult, error) {
	answer, err := s.runtimes.AnswerSideQuestion(ctx, request.SessionID, request.Question)
	return api.SideQuestionResult{Answer: answer}, err
}

func (s *Service) GetArtifact(ctx context.Context, request api.ArtifactRequest) (api.Artifact, error) {
	artifact, err := s.store.Artifact(ctx, strings.TrimSpace(request.Hash))
	if err != nil {
		return api.Artifact{}, mapStateError(err, "artifact")
	}
	value := api.Artifact{Hash: artifact.Hash, Size: artifact.Size, ContentType: artifact.ContentType, Origin: artifact.Origin, CreatedAt: artifact.CreatedAt}
	if artifact.Size > artifactPreviewLimit || !previewableContentType(artifact.ContentType) {
		return value, nil
	}
	data, err := s.store.ReadArtifact(ctx, artifact.Hash)
	if err != nil {
		return api.Artifact{}, err
	}
	value.Previewable = utf8.Valid(data) && !bytes.ContainsRune(data, '\x00')
	if value.Previewable {
		value.Content = data
	}
	return value, nil
}

func previewableContentType(contentType string) bool {
	return contentType == "" || strings.HasPrefix(contentType, "text/") || strings.Contains(contentType, "json")
}

func (s *Service) ListModels(ctx context.Context, request api.ListModelsRequest) (api.ModelCatalog, error) {
	providersCatalog, err := s.providers.ModelCatalog(ctx, request.Refresh)
	if err != nil {
		return api.ModelCatalog{}, err
	}
	result := api.ModelCatalog{Providers: make([]api.Provider, 0, len(providersCatalog))}
	for _, provider := range providersCatalog {
		item := api.Provider{
			ID: provider.ID, Name: provider.Name, Configured: true,
			Endpoint: publicEndpoint(provider.Endpoint), RequiresEndpoint: provider.RequiresEndpoint,
			MetadataState: provider.State, MetadataWarning: provider.Warning, FetchedAt: provider.Metadata.FetchedAt,
		}
		for _, model := range provider.Metadata.Models {
			item.Models = append(item.Models, api.Model{ID: model.ID, Name: model.Name, ContextLength: model.ContextLength})
		}
		result.Providers = append(result.Providers, item)
	}
	return result, nil
}

func (s *Service) ConfigureProvider(ctx context.Context, request api.ConfigureProviderRequest) (api.InitializeResult, error) {
	if err := s.providers.Configure(ctx, providers.ConfigurationUpdate{
		Provider: request.Provider, Model: request.Model, Endpoint: request.Endpoint, APIKey: request.APIKey, Verify: request.Verify,
	}); err != nil {
		return api.InitializeResult{}, err
	}
	return s.Initialize(ctx)
}

func (s *Service) RefreshProviderMetadata(ctx context.Context) (api.InitializeResult, error) {
	if err := s.providers.RefreshMetadata(ctx); err != nil {
		return api.InitializeResult{}, err
	}
	return s.Initialize(ctx)
}

func (s *Service) ProviderUsage(_ context.Context) (api.Usage, error) {
	runtime := s.providers.GetRuntimeConfig()
	if runtime == nil {
		return api.Usage{}, apiError(api.CodeBusy, "provider runtime is unavailable", true)
	}
	usage, metadata := runtime.Usage(), runtime.Metadata()
	result := api.Usage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CostUSD: usage.CostUSD, ContextLimit: runtime.ContextLimit()}
	if metadata.Account != nil {
		total, used, remaining := metadata.Account.TotalCredits, metadata.Account.TotalUsage, metadata.Account.TotalCredits-metadata.Account.TotalUsage
		result.TotalCredits, result.CreditsUsed, result.CreditsRemain = &total, &used, &remaining
	}
	return result, nil
}

func (s *Service) ConfigureEmbeddings(_ context.Context, request api.ConfigureEmbeddingsRequest) error {
	if err := s.providers.UpdateEmbeddingSettings(request.CredentialProvider, request.Endpoint, request.Model); err != nil {
		return err
	}
	settings, err := s.providers.EmbeddingSettings()
	if err != nil {
		return err
	}
	var provider repository.EmbeddingProvider
	if settings.Endpoint != "" && settings.Model != "" && settings.APIKey != "" {
		provider = repository.OpenAICompatibleEmbeddings{Endpoint: settings.Endpoint, ModelName: settings.Model, APIKey: settings.APIKey}
	}
	s.repository.SetEmbeddingProvider(provider)
	return nil
}

func (s *Service) ReloadConfiguration(ctx context.Context) (api.InitializeResult, error) {
	if err := s.providers.Initialize(ctx); err != nil {
		return api.InitializeResult{}, err
	}
	return s.Initialize(ctx)
}

func (s *Service) ListTools(_ context.Context, request api.SessionRequest) ([]api.Tool, error) {
	mode := tools.ApprovalBatman
	if request.SessionID != "" {
		if session, err := agent.LoadSession(s.workspace, request.SessionID); err == nil {
			mode = tools.NormalizeApprovalMode(session.ApprovalMode)
		}
	}
	registered := s.registry.All()
	result := make([]api.Tool, 0, len(registered))
	for _, tool := range registered {
		descriptor, err := s.registry.Descriptor(tool.Name())
		if err != nil {
			return nil, err
		}
		result = append(result, api.Tool{Name: tool.Name(), Description: tool.Description(), Approval: tools.ApprovalPolicyLabel(mode, descriptor), ParallelSafe: descriptor.ParallelSafe})
	}
	return result, nil
}

func (s *Service) ToolActivity(_ context.Context, request api.SessionRequest) ([]api.ToolActivity, error) {
	items := s.runtimes.Recent()
	if strings.TrimSpace(request.SessionID) != "" {
		items = s.runtimes.RecentSession(request.SessionID)
	}
	result := make([]api.ToolActivity, 0, len(items))
	for _, item := range items {
		result = append(result, api.ToolActivity{Time: item.Time, Tool: item.Tool, Status: item.Status, Message: item.Message})
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Time.Before(result[j].Time) })
	return result, nil
}

func (s *Service) WorkspaceStatus(ctx context.Context) (api.WorkspaceStatus, error) {
	result, err := (&toolgit.GitStatus{}).Execute(tools.WithWorkspace(ctx, s.workspace), toolgit.GitStatusInput{Directory: "."})
	status := api.WorkspaceStatus{Workspace: s.workspace}
	if err != nil || result == nil || !result.Success {
		status.Error = "not a git workspace"
		return status, nil
	}
	data, err := json.Marshal(result.Data)
	if err != nil {
		return status, err
	}
	var value toolgit.GitStatusOutput
	if err := json.Unmarshal(data, &value); err != nil {
		return status, err
	}
	status.Branch, status.Changed, status.Git, status.Ready = value.Branch, len(value.Staged)+len(value.Modified)+len(value.Untracked), true, true
	return status, nil
}

func (s *Service) WorkspaceDiff(ctx context.Context) (api.Diff, error) {
	data, err := runWorkspaceCommand(ctx, s.workspace, "git", "diff", "HEAD")
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		data, err = runWorkspaceCommand(ctx, s.workspace, "git", "diff")
	}
	if err != nil {
		return api.Diff{}, err
	}
	content := string(data)
	return api.Diff{Content: content, Summary: summarizeDiff(content)}, nil
}

func (s *Service) Health(_ context.Context) (api.HealthReport, error) {
	report := api.HealthReport{Workspace: s.workspace}
	probe, err := os.CreateTemp(s.workspace, ".supremo-doctor-*")
	if err == nil {
		name := probe.Name()
		err = errors.Join(probe.Close(), os.Remove(name))
	}
	report.Checks = append(report.Checks, healthCheck("workspace write", err))
	for _, binary := range []string{"git", "go"} {
		_, err := exec.LookPath(binary)
		report.Checks = append(report.Checks, healthCheck(binary, err))
	}
	runtime := s.providers.GetRuntimeConfig()
	providerErr := errors.New("provider is unavailable")
	if runtime != nil && runtime.CredentialConfigured() {
		providerErr = nil
	}
	report.Checks = append(report.Checks, healthCheck("provider credential", providerErr))
	_, hookErr := os.Stat(filepath.Join(s.workspace, ".githooks", "pre-commit"))
	report.Checks = append(report.Checks, healthCheck("pre-commit hook", hookErr))
	return report, nil
}

func healthCheck(name string, err error) api.HealthCheck {
	if err == nil {
		return api.HealthCheck{Name: name, Status: "ok"}
	}
	return api.HealthCheck{Name: name, Status: "failed", Message: err.Error()}
}

func (s *Service) ContextStatus(ctx context.Context, request api.ContextStatusRequest) (api.ContextStatus, error) {
	manifest, err := s.compiler.LatestManifest(ctx, request.SessionID)
	if err != nil {
		return api.ContextStatus{}, err
	}
	result := api.ContextStatus{RequestID: manifest.RequestID, EstimatedUsed: manifest.Budget.EstimatedUsed, InputBudget: manifest.Budget.InputBudget,
		OutputReserve: manifest.Budget.OutputReserve, SafetyReserve: manifest.Budget.SafetyReserve, Rejected: len(manifest.IR.Rejected), ArtifactID: manifest.ArtifactID}
	session, err := agent.LoadSession(s.workspace, request.SessionID)
	if err == nil {
		working, workingErr := s.compiler.ActiveWorkingSet(ctx, request.SessionID, session.ActiveTaskID)
		if workingErr == nil {
			result.WorkingSetItems, result.Generation = len(working.Items), int(working.Generation)
		}
	}
	if request.Detailed {
		for _, item := range manifest.IR.Items {
			result.Items = append(result.Items, api.ContextItem{Layer: string(item.Layer), Kind: item.Kind, ID: item.ID, Tokens: item.EstimatedTokens, Reason: item.Reason.Code})
		}
	}
	return result, nil
}

func (s *Service) IndexStatus(ctx context.Context) (api.IndexStatus, error) {
	settings, configured, err := s.repository.SemanticStatus(ctx)
	if err != nil {
		return api.IndexStatus{}, err
	}
	ready, dirty, indexErr := s.repository.Status()
	result := api.IndexStatus{Ready: ready, Dirty: dirty, Semantic: settings.Enabled, Configured: configured}
	if indexErr != nil {
		result.Error = indexErr.Error()
	}
	return result, nil
}

func (s *Service) UpdateIndex(ctx context.Context, request api.UpdateIndexRequest) (api.IndexStatus, error) {
	if err := s.repository.SetSemantic(ctx, request.Semantic); err != nil {
		return api.IndexStatus{}, err
	}
	return s.IndexStatus(ctx)
}

func (s *Service) InitializeWorkspace(ctx context.Context) (api.WorkspaceStatus, error) {
	if _, err := agent.InitializeWorkspace(s.workspace); err != nil {
		return api.WorkspaceStatus{}, err
	}
	return s.WorkspaceStatus(ctx)
}

func runWorkspaceCommand(ctx context.Context, workspace, command string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = workspace
	return cmd.Output()
}

func summarizeDiff(content string) string {
	files, additions, deletions := 0, 0, 0
	for _, line := range strings.Split(content, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			files++
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			additions++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			deletions++
		}
	}
	if files == 0 {
		return "No uncommitted changes"
	}
	return fmt.Sprintf("%d files · +%d −%d", files, additions, deletions)
}
