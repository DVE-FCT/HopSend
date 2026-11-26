package controlv1

// PathVersion is the last explicitly published immutable snapshot for a path.
// It never asserts that the live source still contains the same bytes.
type PathVersion struct {
	ShareID     string `json:"share_id"`
	Path        string `json:"path"`
	ManifestID  string `json:"manifest_id"`
	ContentID   string `json:"content_id"`
	Name        string `json:"name"`
	SizeBytes   int64  `json:"size_bytes"`
	PayloadKind string `json:"payload_kind"`
	PublishedAt string `json:"published_at"`
}
