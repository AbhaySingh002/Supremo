// Package state owns Supremo's durable, workspace-local state. It deliberately
// contains no agent or provider dependencies so the storage engine can outlive
// the coding-agent domain built on top of it.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var ErrConflict = errors.New("state version conflict")

// Repository groups the durable boundaries consumed by higher-level code.
// SQLite is intentionally hidden behind this interface.
type Repository interface {
	EventStore
	SessionStore
	StateStore
	ArtifactStore
	WorkspaceStore
	FileStateStore
	RepositoryIndexStore
	ObservationStore
}

type ObservationStore interface {
	SaveObservation(context.Context, Observation) (Observation, error)
	Observations(context.Context, string, string) ([]Observation, error)
	ObservationByFingerprint(context.Context, string, string) (Observation, bool, error)
}

type EventStore interface {
	AppendEvent(context.Context, EventInput) (Event, error)
	Events(context.Context, EventQuery) ([]Event, error)
	RebuildCurrentState(context.Context) error
}

type SessionStore interface {
	SaveSession(context.Context, SessionInput) (Session, error)
	Session(context.Context, string) (Session, error)
	Sessions(context.Context, bool) ([]Session, error)
	ArchiveSession(context.Context, string) error
	AppendMessage(context.Context, MessageInput) (Message, error)
	Messages(context.Context, string, bool) ([]Message, error)
	ArchiveMessages(context.Context, string) error
}

type StateStore interface {
	SaveDocument(context.Context, DocumentInput) (Document, error)
	DeleteDocument(context.Context, DocumentDeleteInput) error
	Document(context.Context, string, string) (Document, error)
	Documents(context.Context, string, string) ([]Document, error)
	CreateClaim(context.Context, ClaimInput) (Claim, error)
	SupersedeClaim(context.Context, string, ClaimInput) (Claim, error)
	Claims(context.Context, string, bool) ([]Claim, error)
}

type ArtifactStore interface {
	PutArtifact(context.Context, ArtifactInput) (Artifact, error)
	Artifact(context.Context, string) (Artifact, error)
	ReadArtifact(context.Context, string) ([]byte, error)
}

type WorkspaceStore interface {
	WorkspaceID() string
	Root() string
	ObserveWorkspace(context.Context, WorkspaceSnapshot) (WorkspaceRevision, error)
	WorkspaceMemory(context.Context) (string, error)
	SetWorkspaceMemory(context.Context, string) error
}

type FileStateStore interface {
	ObserveFile(context.Context, FileObservation) (FileVersion, error)
	RenameFile(context.Context, FileRename) error
	FileVersions(context.Context, string) ([]FileVersion, error)
}

// RepositoryIndexStore persists deterministic repository-derived data. The
// repository package owns scanning/parsing; this boundary keeps SQL local.
type RepositoryIndexStore interface {
	RepositoryFiles(context.Context) ([]RepositoryFileState, error)
	LatestRepositoryRevision(context.Context) (RepositoryRevision, error)
	TouchRepositoryFile(context.Context, RepositoryFileState) error
	BeginRepositoryRevision(context.Context, RepositoryRevisionInput) (RepositoryRevision, error)
	ApplyRepositoryFile(context.Context, RepositoryFileInput) (RepositoryFileState, error)
	MarkRepositoryFileDeleted(context.Context, RepositoryDeleteInput) error
	RepositoryCandidates(context.Context, RepositoryLookup) ([]RepositoryCandidate, error)
	RepositoryCandidatesByID(context.Context, []string) ([]RepositoryCandidate, error)
	RepositorySymbolCandidatesByID(context.Context, []string) ([]RepositoryCandidate, error)
	RepositoryCurrentChunks(context.Context) ([]RepositoryCandidate, error)
	RepositoryNeighbors(context.Context, string, RelationDirection, int) ([]RepositoryRelation, error)
	RepositoryRepresentations(context.Context, string) ([]RepositoryRepresentation, error)
	RepositorySemanticSettings(context.Context) (SemanticSettings, error)
	SetRepositorySemanticSettings(context.Context, SemanticSettings) error
	PutRepositoryEmbeddings(context.Context, []RepositoryEmbeddingInput) error
	RepositoryEmbeddings(context.Context, string) ([]RepositoryEmbedding, error)
}

type EventInput struct {
	ID             string
	SessionID      string
	AgentID        string
	Type           string
	CorrelationID  string
	CausationID    string
	IdempotencyKey string
	PayloadVersion int
	Payload        any
	CreatedAt      time.Time
}

// DocumentDeleteInput deletes every stored version of one document after its
// current version has been checked. Events and artifacts deliberately remain
// durable audit data.
type DocumentDeleteInput struct {
	ID              string
	Kind            string
	ExpectedVersion int64
	Event           EventInput
}

type EventQuery struct {
	SessionID string
	Type      string
	After     int64
	Limit     int
}

type Event struct {
	Sequence       int64
	ID             string
	WorkspaceID    string
	SessionID      string
	AgentID        string
	Type           string
	CorrelationID  string
	CausationID    string
	IdempotencyKey string
	PayloadVersion int
	Payload        json.RawMessage
	CreatedAt      time.Time
}

type SessionInput struct {
	ID              string
	Name            string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Status          string
	CurrentTaskID   string
	Provider        string
	Model           string
	Metadata        json.RawMessage
	Data            json.RawMessage
	ExpectedVersion int64
	Event           EventInput
	RelatedEvents   []EventInput
}

type Session struct {
	ID            string
	WorkspaceID   string
	Name          string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Status        string
	CurrentTaskID string
	Provider      string
	Model         string
	Metadata      json.RawMessage
	Data          json.RawMessage
	Version       int64
}

type MessageInput struct {
	ID            string
	SessionID     string
	Role          string
	TaskID        string
	State         string
	ParentID      string
	Parts         []MessagePartInput
	CreatedAt     time.Time
	Event         EventInput
	RelatedEvents []EventInput
}

type MessagePartInput struct {
	Kind       string
	Text       string
	ArtifactID string
	Metadata   json.RawMessage
}

type MessagePart struct {
	Ordinal    int
	Kind       string
	Text       string
	ArtifactID string
	Metadata   json.RawMessage
}

type Message struct {
	ID        string
	SessionID string
	Sequence  int64
	Role      string
	TaskID    string
	State     string
	ParentID  string
	CreatedAt time.Time
	Parts     []MessagePart
}

// Document is an immutable revision of structured agent data such as a task,
// plan, requirement, decision, assumption, error, or test result.
type DocumentInput struct {
	ID              string
	Kind            string
	SessionID       string
	Status          string
	Payload         json.RawMessage
	Provenance      Provenance
	ExpectedVersion int64
	Event           EventInput
	Events          []EventInput
}

type Document struct {
	ID         string
	Kind       string
	SessionID  string
	Status     string
	Payload    json.RawMessage
	Provenance Provenance
	Version    int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Authority string

const (
	AuthorityUser           Authority = "user_instruction"
	AuthorityFilesystem     Authority = "filesystem"
	AuthorityRuntime        Authority = "runtime_result"
	AuthorityRepository     Authority = "repository_metadata"
	AuthorityDerived        Authority = "deterministic_derivation"
	AuthorityDecision       Authority = "accepted_decision"
	AuthorityLLMObservation Authority = "llm_observation"
	AuthorityLLMSummary     Authority = "llm_summary"
)

type Provenance struct {
	SourceEventID       string    `json:"source_event_id,omitempty"`
	Source              string    `json:"source,omitempty"`
	Actor               string    `json:"actor,omitempty"`
	Authority           Authority `json:"authority,omitempty"`
	WorkspaceRevisionID string    `json:"workspace_revision_id,omitempty"`
	EvidenceArtifactIDs []string  `json:"evidence_artifact_ids,omitempty"`
	ObservedAt          time.Time `json:"observed_at,omitempty"`
	FreshUntil          time.Time `json:"fresh_until,omitempty"`
	SupersedesID        string    `json:"supersedes_id,omitempty"`
}

type ClaimInput struct {
	ID         string
	Kind       string
	Statement  string
	Scope      string
	Status     string
	Confidence float64
	Provenance Provenance
	Event      EventInput
}

type Claim struct {
	ID             string
	LineageID      string
	Kind           string
	Statement      string
	Scope          string
	Status         string
	Confidence     float64
	Provenance     Provenance
	SupersedesID   string
	SupersededByID string
	CreatedAt      time.Time
}

type ArtifactInput struct {
	Data        []byte
	ContentType string
	Origin      string
	Event       EventInput
}

type Artifact struct {
	Hash        string
	Size        int64
	ContentType string
	Origin      string
	CreatedAt   time.Time
}

type WorkspaceSnapshot struct {
	Head       string
	Branch     string
	Dirty      bool
	Metadata   json.RawMessage
	ObservedAt time.Time
}

type WorkspaceRevision struct {
	ID          string
	WorkspaceID string
	Head        string
	Branch      string
	Dirty       bool
	Metadata    json.RawMessage
	ObservedAt  time.Time
}

type FileObservation struct {
	Path                string
	Data                []byte
	Deleted             bool
	ModifiedAt          time.Time
	WorkspaceRevisionID string
	Event               EventInput
}

// FileRename retains one stable file identity when a tool reports a rename.
// The prior and new paths stay queryable through immutable file versions.
type FileRename struct {
	OldPath             string
	NewPath             string
	WorkspaceRevisionID string
	Event               EventInput
}

type FileVersion struct {
	ID                  string
	FileID              string
	Path                string
	Hash                string
	Size                int64
	Deleted             bool
	ModifiedAt          time.Time
	WorkspaceRevisionID string
	ArtifactID          string
	ObservedAt          time.Time
}

// RepositoryRevision is a scanner generation linked to the Phase 1 world
// revision when one is available.
type RepositoryRevisionInput struct {
	WorkspaceRevisionID string
	Head                string
	Branch              string
	Dirty               bool
	ScannerVersion      string
	ObservedAt          time.Time
}

type RepositoryRevision struct {
	ID                  string
	WorkspaceRevisionID string
	Head                string
	Branch              string
	Dirty               bool
	ScannerVersion      string
	ObservedAt          time.Time
}

type RepositoryFileState struct {
	FileID               string
	FileVersionID        string
	Path                 string
	Hash                 string
	Size                 int64
	ModifiedAt           time.Time
	Language             string
	RepositoryRevisionID string
	Deleted              bool
}

type RepositorySymbolInput struct {
	StableKey     string
	Name          string
	QualifiedName string
	Kind          string
	Exported      bool
	StartLine     int
	StartColumn   int
	EndLine       int
	EndColumn     int
	Signature     string
	DocComment    string
}

type RepositoryChunkInput struct {
	StableKey   string
	SymbolKey   string
	Kind        string
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int
	Content     string
}

type RepositoryRelationInput struct {
	Type            string
	SourceSymbolKey string
	TargetSymbolKey string
	TargetName      string
	Confidence      float64
	Provenance      Provenance
}

type RepositorySummaryInput struct {
	Scope            string
	TargetStableKey  string
	Content          string
	GenerationMethod string
	GenerationModel  string
	Confidence       float64
}

type RepositoryFileInput struct {
	Observation          FileObservation
	RepositoryRevisionID string
	Language             string
	Symbols              []RepositorySymbolInput
	Chunks               []RepositoryChunkInput
	Relations            []RepositoryRelationInput
	Summaries            []RepositorySummaryInput
}

type RepositoryDeleteInput struct {
	Path                 string
	RepositoryRevisionID string
	Event                EventInput
}

type RelationDirection string

const (
	RelationOutgoing RelationDirection = "outgoing"
	RelationIncoming RelationDirection = "incoming"
	RelationBoth     RelationDirection = "both"
)

type RepositoryLookup struct {
	Query        string
	ScopePath    string
	Kind         string
	Limit        int
	ExactOnly    bool
	Prefix       bool
	FullText     bool
	IncludeStale bool
}

type RepositoryCandidate struct {
	ID                 string
	Type               string
	FileID             string
	FileVersionID      string
	SymbolID           string
	Path               string
	Name               string
	QualifiedName      string
	Kind               string
	Hash               string
	StartLine          int
	StartColumn        int
	EndLine            int
	EndColumn          int
	Signature          string
	Content            string
	Score              float64
	BM25               float64
	GraphDistance      int
	SemanticSimilarity float64
	Provenance         Provenance
	Representations    []RepresentationLevel
}

type RepositoryRelation struct {
	ID                    string
	Type                  string
	SourceSymbolID        string
	TargetSymbolID        string
	TargetName            string
	EvidenceFileVersionID string
	Confidence            float64
	Provenance            Provenance
}

type RepresentationLevel string

const (
	RepresentationR0 RepresentationLevel = "R0"
	RepresentationR1 RepresentationLevel = "R1"
	RepresentationR2 RepresentationLevel = "R2"
	RepresentationR3 RepresentationLevel = "R3"
	RepresentationR4 RepresentationLevel = "R4"
	RepresentationR5 RepresentationLevel = "R5"
)

type RepositoryRepresentation struct {
	Level      RepresentationLevel
	Content    string
	ArtifactID string
	StartLine  int
	EndLine    int
	SourceHash string
	Provenance Provenance
}

type SemanticSettings struct {
	Enabled   bool
	UpdatedAt time.Time
}

type RepositoryEmbeddingInput struct {
	ChunkID    string
	SourceHash string
	Model      string
	Vector     []byte
	Dimensions int
}

type RepositoryEmbedding struct {
	ID         string
	ChunkID    string
	SourceHash string
	Model      string
	Vector     []byte
	Dimensions int
	CreatedAt  time.Time
}
