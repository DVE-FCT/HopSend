package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	manifestv1 "github.com/DVE-FCT/HopSend/contracts/manifest/v1"
	"github.com/DVE-FCT/HopSend/internal/buildinfo"
	"github.com/DVE-FCT/HopSend/internal/manifest"
)

type principal struct {
	tokenHash [32]byte
	shares    map[string]bool
}
type Server struct {
	HTTP        *http.Server
	roots       map[string]*os.Root
	identities  []principal
	contentRoot *os.Root
}

func New(cfg Config) (*Server, error) {
	if cfg.Listen == "" || cfg.CertificateFile == "" || cfg.KeyFile == "" || len(cfg.Credentials) == 0 {
		return nil, errors.New("Agent 缺少监听、TLS 或身份配置")
	}
	if _, err := tls.LoadX509KeyPair(cfg.CertificateFile, cfg.KeyFile); err != nil {
		return nil, errors.New("Agent TLS 证书或私钥不可用")
	}
	s := &Server{roots: make(map[string]*os.Root)}
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
		p := principal{tokenHash: hash, shares: make(map[string]bool)}
		for _, id := range credential.ShareIDs {
			if s.roots[id] == nil {
				return nil, errors.New("身份引用了不存在的共享")
			}
			p.shares[id] = true
		}
		s.identities = append(s.identities, p)
	}
	s.HTTP = &http.Server{Addr: cfg.Listen, Handler: http.HandlerFunc(s.serve), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	ok = true
	return s, nil
}

func (s *Server) Close() error {
	var errs []error
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
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		fail(w, 405, "method_not_allowed", "此接口仅支持 GET")
		return
	}
	switch r.URL.Path {
	case "/v1/health":
		send(w, 200, v1.Health{Status: "ok", Service: "hopsend-agent", Version: buildinfo.Version, APIVersion: "v1"})
	case "/v1/capabilities":
		operations := []string{"health", "capabilities", "shares", "list_entries"}
		if s.contentRoot != nil {
			operations = append(operations, "published_manifest")
		}
		send(w, 200, v1.Capabilities{APIVersion: "v1", Operations: operations, DataTransport: "external_sftp", UploadSupported: false})
	case "/v1/shares":
		items := make([]v1.Share, 0, len(p.shares))
		for id := range p.shares {
			items = append(items, v1.Share{ID: id, Access: "read_only"})
		}
		slices.SortFunc(items, func(a, b v1.Share) int { return strings.Compare(a.ID, b.ID) })
		send(w, 200, v1.ShareList{Shares: items})
	default:
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
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

func (s *Server) entries(w http.ResponseWriter, r *http.Request, id string) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "."
	}
	if !fs.ValidPath(path) || strings.ContainsAny(path, `\:`) || strings.ContainsRune(path, 0) {
		fail(w, 400, "invalid_path", "路径必须位于授权共享内")
		return
	}
	root := s.roots[id]
	// Refuse symlink/reparse traversal even inside the root; os.Root additionally
	// constrains resolution if a component is concurrently replaced.
	if path != "." {
		prefix := ""
		for _, component := range strings.Split(path, "/") {
			if prefix != "" {
				prefix += "/"
			}
			prefix += component
			info, err := root.Lstat(prefix)
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				fail(w, 403, "path_denied", "路径不可访问")
				return
			}
		}
	}
	file, err := root.Open(path)
	if err != nil {
		fail(w, 403, "path_denied", "路径不可访问")
		return
	}
	defer file.Close()
	items, err := file.ReadDir(1001)
	if err != nil && err != io.EOF {
		fail(w, 400, "not_directory", "目标不是可读取目录")
		return
	}
	if len(items) > 1000 {
		fail(w, 413, "directory_too_large", "目录超过首版单次 1000 项上限")
		return
	}
	entries := make([]v1.Entry, 0, len(items))
	for _, item := range items {
		info, err := item.Info()
		if err != nil {
			fail(w, 403, "path_denied", "目录内容不可读取")
			return
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.Mode().IsRegular() && !info.IsDir()) {
			continue
		}
		size := info.Size()
		if info.IsDir() {
			size = 0
		}
		entries = append(entries, v1.Entry{Name: item.Name(), Directory: info.IsDir(), SizeBytes: size})
	}
	slices.SortFunc(entries, func(a, b v1.Entry) int { return strings.Compare(a.Name, b.Name) })
	send(w, 200, v1.EntryList{ShareID: id, Path: path, Entries: entries})
}
