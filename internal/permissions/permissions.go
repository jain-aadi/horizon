package permissions

// Permission represents a capability an agent may request.
type Permission string

const (
	PermFilesystemRead  Permission = "filesystem:read"
	PermFilesystemWrite Permission = "filesystem:write"
	PermNetworkAccess   Permission = "network:access"
	PermShellAccess     Permission = "shell:access"
	PermSecretAccess    Permission = "secret:access"
)

// PermissionSet is a collection of permissions.
type PermissionSet map[Permission]bool

// Has checks if a permission is granted.
func (p PermissionSet) Has(perm Permission) bool {
	return p[perm]
}

// Grant adds a permission.
func (p PermissionSet) Grant(perm Permission) {
	p[perm] = true
}

// Revoke removes a permission.
func (p PermissionSet) Revoke(perm Permission) {
	delete(p, perm)
}

// DefaultPermissions returns the default permission set for agents.
func DefaultPermissions() PermissionSet {
	return PermissionSet{
		PermFilesystemRead:  true,
		PermFilesystemWrite: false,
		PermNetworkAccess:   true,
		PermShellAccess:     false,
		PermSecretAccess:    false,
	}
}

// Validate checks if a requested permission is known.
func Validate(perm Permission) bool {
	switch perm {
	case PermFilesystemRead, PermFilesystemWrite, PermNetworkAccess, PermShellAccess, PermSecretAccess:
		return true
	default:
		return false
	}
}