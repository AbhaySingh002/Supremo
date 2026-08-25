package repository

import "context"

type contextKey struct{}

func WithService(ctx context.Context, service *Service) context.Context {
	if service == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, service)
}

func FromContext(ctx context.Context) *Service {
	service, _ := ctx.Value(contextKey{}).(*Service)
	return service
}
