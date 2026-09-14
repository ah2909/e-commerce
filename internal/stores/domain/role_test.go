package domain

import "testing"

func TestRolePermissionMatrixIsDenyByDefault(t *testing.T) {
	permissions := []Permission{PermissionStoreView, PermissionStoreManage, PermissionMembershipsView, PermissionMembershipsManage, PermissionCatalogManage, PermissionPricingManage, PermissionInventoryView, PermissionInventoryManage, PermissionPaymentConfirm, PermissionPaymentReconcile, "unknown"}
	want := map[Role][]bool{
		RoleOwner: {true, true, true, true, true, true, true, true, true, true, false},
		RoleAdmin: {true, true, true, false, true, true, true, true, true, true, false},
		RoleStaff: {true, false, false, false, false, false, false, false, false, false, false},
		"unknown": {false, false, false, false, false, false, false, false, false, false, false},
	}
	for role, expected := range want {
		for index, permission := range permissions {
			if got := role.Allows(permission); got != expected[index] {
				t.Errorf("%q.Allows(%q) = %v", role, permission, got)
			}
		}
	}
}
