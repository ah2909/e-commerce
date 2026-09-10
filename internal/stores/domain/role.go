package domain

import "errors"

var ErrInvalidRole = errors.New("invalid membership role")

type Role string
type Permission string

const (
	RoleOwner Role = "owner"
	RoleAdmin Role = "admin"
	RoleStaff Role = "staff"

	PermissionStoreView         Permission = "store.view"
	PermissionStoreManage       Permission = "store.manage"
	PermissionMembershipsView   Permission = "memberships.view"
	PermissionMembershipsManage Permission = "memberships.manage"
	PermissionCatalogManage     Permission = "catalog.manage"
)

func ParseRole(value string) (Role, error) {
	role := Role(value)
	if role != RoleOwner && role != RoleAdmin && role != RoleStaff {
		return "", ErrInvalidRole
	}
	return role, nil
}

func (role Role) Allows(permission Permission) bool {
	switch role {
	case RoleOwner:
		return permission == PermissionStoreView || permission == PermissionStoreManage || permission == PermissionMembershipsView || permission == PermissionMembershipsManage || permission == PermissionCatalogManage
	case RoleAdmin:
		return permission == PermissionStoreView || permission == PermissionStoreManage || permission == PermissionMembershipsView || permission == PermissionCatalogManage
	case RoleStaff:
		return permission == PermissionStoreView
	default:
		return false
	}
}
