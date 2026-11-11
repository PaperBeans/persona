package proto

// RepositoryIdentity is the node's local Git identity, requested through git.status with identity_only.
// Origin is a canonical identifier, never a URL containing credentials.
type RepositoryIdentity struct {
	PathResolved bool   `json:"path_resolved,omitempty"`
	Version      int    `json:"version"`
	IsRepo       bool   `json:"is_repo"`
	Cwd          string `json:"cwd"`
	Root         string `json:"root,omitempty"`
	CommonDir    string `json:"common_dir,omitempty"`
	HeadOID      string `json:"head_oid,omitempty"`
	Origin       string `json:"origin,omitempty"`
}
