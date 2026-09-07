package authn

import "context"

type contextKey struct{}

type Principal struct {
	userID  string
	storeID string
}

func NewPrincipal(userID, storeID string) Principal {
	return Principal{userID: userID, storeID: storeID}
}

func (principal Principal) UserID() string  { return principal.userID }
func (principal Principal) StoreID() string { return principal.storeID }

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, principal)
}

func FromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(contextKey{}).(Principal)
	return principal, ok
}
