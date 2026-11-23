// Package manifestv1 defines immutable, content-addressed file manifests.
package manifestv1

type Chunk struct {
	Index  int    `json:"index"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	PayloadKind    string  `json:"payload_kind,omitempty"`
	SchemaVersion  int     `json:"schema_version"`
	ManifestID     string  `json:"manifest_id"`
	ContentID      string  `json:"content_id"`
	ShareID        string  `json:"share_id"`
	Name           string  `json:"name"`
	SizeBytes      int64   `json:"size_bytes"`
	ChunkSizeBytes int64   `json:"chunk_size_bytes"`
	Chunks         []Chunk `json:"chunks"`
}

type Published struct {
	Manifest Manifest `json:"manifest"`
	SFTPPath string   `json:"sftp_path"`
}
