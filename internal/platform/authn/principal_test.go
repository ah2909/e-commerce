package authn

import "testing"

func TestUserPrincipalIsStoreBound(t *testing.T) {
	principal := NewUserPrincipal("store-id", "user-id", "session-id")
	if principal.Kind() != KindUser || principal.StoreID() != "store-id" || principal.UserID() != "user-id" || principal.SessionID() != "session-id" || principal.GuestID() != "" {
		t.Fatalf("user principal = %#v", principal)
	}
}

func TestGuestPrincipalIsDistinctAndStoreBound(t *testing.T) {
	principal := NewGuestPrincipal("store-id", "guest-id", "session-id")
	if principal.Kind() != KindGuest || principal.StoreID() != "store-id" || principal.GuestID() != "guest-id" || principal.SessionID() != "session-id" {
		t.Fatalf("guest principal = %#v", principal)
	}
	if principal.UserID() != "" {
		t.Fatalf("guest user ID = %q", principal.UserID())
	}
}

func TestPlatformAdminPrincipalIsNotTenantBound(t *testing.T) {
	principal := NewPlatformAdminPrincipal("deployment")
	if principal.Kind() != KindPlatformAdmin || principal.PlatformID() != "deployment" {
		t.Fatalf("platform principal = %#v", principal)
	}
	if principal.StoreID() != "" || principal.UserID() != "" || principal.GuestID() != "" || principal.SessionID() != "" {
		t.Fatalf("platform principal has tenant identity = %#v", principal)
	}
}
