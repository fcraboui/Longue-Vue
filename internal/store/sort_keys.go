// sort_keys.go — shared sort-key string constants for the sortSpec/sortVal
// switches in each pg_*.go file. Using named constants keeps goconst happy
// and makes typos in key names a compile-time error.
package store

const (
	sortKeyName             = "name"
	sortKeyCreatedAt        = "created_at"
	sortKeyUpdatedAt        = "updated_at"
	sortKeyPhase            = "phase"
	sortKeyStorageClassName = "storage_class_name"
	sortKeyCsiDriver        = "csi_driver"
	sortKeyCapacity         = "capacity"
	sortKeyRequestedStorage = "requested_storage"
	sortKeyIngressClassName = "ingress_class_name"
	sortKeyType             = "type"
	sortKeyClusterIP        = "cluster_ip"
	sortKeyUsername         = "username"
	sortKeyLastUsedAt       = "last_used_at"
	sortKeyExpiresAt        = "expires_at"
)
