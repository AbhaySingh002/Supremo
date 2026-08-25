package ui

import (
	"context"

	"github.com/AbhaySingh002/supremo/internal/api"
)

type sessionTestClient struct{ api.Client }

func (sessionTestClient) UpdateSession(_ context.Context, request api.UpdateSessionRequest) (api.Session, error) {
	session := api.Session{ID: request.SessionID, Revision: request.ExpectedRevision + 1, ApprovalMode: "batman", Checklist: true, Rewind: true, ProviderRetry: true}
	if request.ApprovalMode != nil {
		session.ApprovalMode = *request.ApprovalMode
	}
	if request.DryRun != nil {
		session.DryRun = *request.DryRun
	}
	if request.PlanMode != nil {
		session.PlanMode = *request.PlanMode
	}
	return session, nil
}

func newTestModel(session api.Session, ctx context.Context, cancel context.CancelFunc) Model {
	if session.ID == "" {
		session.ID = "test-session"
	}
	if session.Name == "" {
		session.Name = session.ID
	}
	if session.ApprovalMode == "" {
		session.ApprovalMode = "batman"
	}
	model := New(nil, ".", session.ID, Options{Context: ctx, Shutdown: cancel})
	model.session = session
	return model
}
