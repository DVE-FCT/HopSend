// Package controlv1 defines the implemented control-plane wire types.
package controlv1

const (
	DefaultEntryPageSize = 100
	MaxEntryPageSize     = 500
)

type Health struct {
	Status     string `json:"status"`
	Service    string `json:"service"`
	Version    string `json:"version"`
	APIVersion string `json:"api_version"`
}

type Capabilities struct {
	APIVersion      string   `json:"api_version"`
	Operations      []string `json:"operations"`
	DataTransport   string   `json:"data_transport"`
	UploadSupported bool     `json:"upload_supported"`
}

type Share struct {
	ID     string `json:"id"`
	Access string `json:"access"`
}
type ShareList struct {
	Shares []Share `json:"shares"`
}
type Entry struct {
	Name      string `json:"name"`
	Directory bool   `json:"directory"`
	SizeBytes int64  `json:"size_bytes"`
}
type EntryList struct {
	ShareID    string  `json:"share_id"`
	Path       string  `json:"path"`
	Entries    []Entry `json:"entries"`
	NextCursor string  `json:"next_cursor"`
	HasMore    bool    `json:"has_more"`
}
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Error struct {
	Error ErrorDetail `json:"error"`
}
