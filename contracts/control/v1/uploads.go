package controlv1

import manifestv1 "github.com/DVE-FCT/HopSend/contracts/manifest/v1"

type CreateUpload struct {
	RequestID  string              `json:"request_id"`
	ShareID    string              `json:"share_id"`
	Target     string              `json:"target"`
	ParentPath string              `json:"parent_path,omitempty"`
	Manifest   manifestv1.Manifest `json:"manifest"`
}

type UploadSession struct {
	ID         string `json:"id"`
	RequestID  string `json:"request_id"`
	ShareID    string `json:"share_id"`
	Target     string `json:"target"`
	ParentPath string `json:"parent_path,omitempty"`
	ManifestID string `json:"manifest_id"`
	State      string `json:"state"`
	SFTPPath   string `json:"sftp_path"`
	Confirmed  []int  `json:"confirmed_chunks"`
	SHA256     string `json:"sha256,omitempty"`
}
