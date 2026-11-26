package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	manifestv1 "github.com/DVE-FCT/HopSend/contracts/manifest/v1"
	"github.com/DVE-FCT/HopSend/internal/buildinfo"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	"github.com/DVE-FCT/HopSend/internal/storage/sqlite"
)

type principal struct {
	id           string
	tokenHash    [32]byte
	shares       map[string]bool
	uploadShares map[string]bool
}

type Server struct {
	HTTP           *http.Server
	roots          map[string]*os.Root
	identities     []principal
	contentRoot    *os.Root
	writable       map[string]bool
	uploadStore    *sqlite.Store
	uploadCreateMu sync.Mutex
	uploadLocks    sync.Map
	entryCursorKey [32]byte
}

func New(cfg Config) (*Server, error) {
	if cfg.Listen == "" || cfg.CertificateFile == "" || cfg.KeyFile == "" || len(cfg.Credentials) == 0 {
		return nil, errors.New("Agent 缺少监听、TLS 或身份配置")
	}
	if _, err := tls.LoadX509KeyPair(cfg.CertificateFile, cfg.KeyFile); err != nil {
		return nil, errors.New("Agent TLS 证书或私钥不可用")
	}
	s := &Server{roots: make(map[string]*os.Root), writable: make(map[string]bool)}
	if _, err := rand.Read(s.entryCursorKey[:]); err != nil {
		return nil, errors.New("目录游标签名初始化失败")
	}
	ok := false
	defer func() {
		if !ok {
			s.Close()
		}
	}()
	for _, share := range cfg.Shares {
		if !validID.MatchString(share.ID) || s.roots[share.ID] != nil {
			return nil, errors.New("共享 ID 无效或重复")
		}
		root, err := os.OpenRoot(share.Path)
		if err != nil {
			return nil, errors.New("共享根目录不可用")
		}
		s.roots[share.ID] = root
		if share.Writable {
			s.writable[share.ID] = true
		}
	}
	if len(s.writable) > 0 {
		directory := cfg.StateDirectory
		if directory == "" {
			directory = filepath.Join(filepath.Dir(cfg.KeyFile), "state")
		}
		if err := os.MkdirAll(directory, 0700); err != nil {
			return nil, errors.New("Agent 状态目录不可用")
		}
		var err error
		s.uploadStore, err = sqlite.Open(filepath.Join(directory, "agent.db"))
		if err != nil {
			return nil, errors.New("Agent 上传数据库不可用")
		}
	}
	if cfg.ContentDirectory != "" {
		root, err := os.OpenRoot(cfg.ContentDirectory)
		if err != nil {
			return nil, errors.New("已发布内容目录不可用")
		}
		s.contentRoot = root
	}
	ids := map[string]bool{}
	tokens := map[[32]byte]bool{}
	for _, credential := range cfg.Credentials {
		if !validID.MatchString(credential.ID) || ids[credential.ID] {
			return nil, errors.New("身份 ID 无效或重复")
		}
		ids[credential.ID] = true
		data, err := os.ReadFile(credential.TokenFile)
		if err != nil {
			return nil, errors.New("身份凭据文件不可用")
		}
		token := strings.TrimSpace(string(data))
		decoded, err := hex.DecodeString(token)
		if err != nil || len(decoded) != 32 {
			return nil, errors.New("凭据必须为 32 随机字节的 64 位十六进制编码")
		}
		hash := sha256.Sum256([]byte(token))
		if tokens[hash] {
			return nil, errors.New("不同身份不能复用相同凭据")
		}
		tokens[hash] = true
		p := principal{id: credential.ID, tokenHash: hash, shares: make(map[string]bool), uploadShares: make(map[string]bool)}
		for _, id := range credential.ShareIDs {
			if s.roots[id] == nil {
				return nil, errors.New("身份引用了不存在的共享")
			}
			p.shares[id] = true
		}
		for _, id := range credential.UploadShareIDs {
			if !s.writable[id] {
				return nil, errors.New("上传授权引用了不存在或只读共享")
			}
			p.uploadShares[id] = true
		}
		s.identities = append(s.identities, p)
	}
	s.HTTP = &http.Server{Addr: cfg.Listen, Handler: http.HandlerFunc(s.serve), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	ok = true
	return s, nil
}

func (s *Server) Close() error {
	var errs []error
	if s.uploadStore != nil {
		errs = append(errs, s.uploadStore.Close())
	}
	if s.contentRoot != nil {
		errs = append(errs, s.contentRoot.Close())
	}
	for _, root := range s.roots {
		errs = append(errs, root.Close())
	}
	return errors.Join(errs...)
}

func send(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, code, message string) {
	send(w, status, v1.Error{Error: v1.ErrorDetail{Code: code, Message: message}})
}

func (s *Server) authenticate(r *http.Request) *principal {
	value := r.Header.Get("Authorization")
	if !strings.HasPrefix(value, "Bearer ") || len(value) > 512 {
		return nil
	}
	hash := sha256.Sum256([]byte(strings.TrimPrefix(value, "Bearer ")))
	var found *principal
	for i := range s.identities {
		if subtle.ConstantTimeCompare(hash[:], s.identities[i].tokenHash[:]) == 1 {
			found = &s.identities[i]
		}
	}
	return found
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil {
		fail(w, http.StatusBadRequest, "tls_required", "需要 HTTPS")
		return
	}
	p := s.authenticate(r)
	if p == nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="HopSend"`)
		fail(w, 401, "unauthorized", "凭据缺失或无效")
		return
	}
	if r.URL.Path == "/v1/uploads" || strings.HasPrefix(r.URL.Path, "/v1/uploads/") {
		s.uploadAPI(w, r, p)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		fail(w, 405, "method_not_allowed", "此接口仅支持 GET")
		return
	}
	switch r.URL.Path {
	case "/v1/health":
		send(w, 200, v1.Health{Status: "ok", Service: "hopsend-agent", Version: buildinfo.Version, APIVersion: "v1"})
	case "/v1/capabilities":
		operations := []string{"health", "capabilities", "shares", "list_entries", "list_entries_paginated"}
		if s.contentRoot != nil {
			operations = append(operations, "published_manifest", "published_path_index")
		}
		if s.uploadStore != nil {
			operations = append(operations, "create_upload", "upload_status", "confirm_upload_chunk", "finalize_upload", "upload_parent_path")
		}
		send(w, 200, v1.Capabilities{APIVersion: "v1", Operations: operations, DataTransport: "external_sftp", UploadSupported: s.uploadStore != nil})
	case "/v1/shares":
		items := make([]v1.Share, 0, len(p.shares))
		union := make(map[string]bool)
		for id := range p.shares {
			union[id] = true
		}
		for id := range p.uploadShares {
			union[id] = true
		}
		for id := range union {
			access := "read_only"
			if p.uploadShares[id] {
				access = "write_only"
				if p.shares[id] {
					access = "read_write"
				}
			}
			items = append(items, v1.Share{ID: id, Access: access})
		}
		slices.SortFunc(items, func(a, b v1.Share) int { return strings.Compare(a.ID, b.ID) })
		send(w, 200, v1.ShareList{Shares: items})
	default:
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) == 4 && parts[0] == "v1" && parts[1] == "shares" && parts[3] == "index" && r.URL.Path == "/"+strings.Join(parts, "/") {
			s.pathIndex(w, r, p, parts[2])
			return
		}
		if len(parts) == 5 && parts[0] == "v1" && parts[1] == "shares" && parts[3] == "manifests" && r.URL.Path == "/"+strings.Join(parts, "/") {
			id := parts[2]
			if !p.shares[id] || s.contentRoot == nil {
				fail(w, 404, "manifest_not_found", "内容版本不可访问")
				return
			}
			m, err := manifest.Load(s.contentRoot, parts[4])
			if err != nil || m.ShareID != id {
				fail(w, 404, "manifest_not_found", "内容版本不可访问")
				return
			}
			path := filepath.Join(s.contentRoot.Name(), "objects", m.ContentID)
			if !filepath.IsAbs(path) {
				fail(w, 500, "content_configuration", "内容目录需为绝对路径")
				return
			}
			path = filepath.ToSlash(path)
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			send(w, 200, manifestv1.Published{Manifest: m, SFTPPath: path})
			return
		}
		if len(parts) != 4 || parts[0] != "v1" || parts[1] != "shares" || parts[3] != "entries" || r.URL.Path != "/"+strings.Join(parts, "/") {
			fail(w, 404, "not_found", "接口不存在")
			return
		}
		id := parts[2]
		if !p.shares[id] || s.roots[id] == nil {
			fail(w, 404, "share_not_found", "共享不可访问")
			return
		}
		s.entries(w, r, id)
	}
}
