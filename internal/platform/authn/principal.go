package authn

import "context"

type contextKey struct{}

type Kind string

const (
	KindUser          Kind = "user"
	KindGuest         Kind = "guest"
	KindPlatformAdmin Kind = "platform_admin"
)

type Principal struct {
	kind       Kind
	userID     string
	guestID    string
	storeID    string
	sessionID  string
	platformID string
}

func NewUserPrincipal(storeID, userID, sessionID string) Principal {
	return Principal{kind: KindUser, storeID: storeID, userID: userID, sessionID: sessionID}
}

func NewGuestPrincipal(storeID, guestID, sessionID string) Principal {
	return Principal{kind: KindGuest, storeID: storeID, guestID: guestID, sessionID: sessionID}
}

func NewPlatformAdminPrincipal(platformID string) Principal {
	return Principal{kind: KindPlatformAdmin, platformID: platformID}
}

func (principal Principal) Kind() Kind         { return principal.kind }
func (principal Principal) UserID() string     { return principal.userID }
func (principal Principal) GuestID() string    { return principal.guestID }
func (principal Principal) StoreID() string    { return principal.storeID }
func (principal Principal) SessionID() string  { return principal.sessionID }
func (principal Principal) PlatformID() string { return principal.platformID }

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, principal)
}

func FromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(contextKey{}).(Principal)
	return principal, ok
}
