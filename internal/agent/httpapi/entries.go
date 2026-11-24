package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
)

const (
	// Bound scan work as well as the response size. This limit is independent of
	// the folder-transfer bundle limit and counts unsupported entries too.
	maxDirectoryScanEntries = 100000
	maxEntryCursorBytes     = 4096
)

type entryCursor struct {
	Version  int    `json:"version"`
	Scope    string `json:"scope"`
	Snapshot string `json:"snapshot"`
	Last     string `json:"last"`
}

type entryFingerprint struct {
	Name      string `json:"name"`
	Directory bool   `json:"directory"`
	SizeBytes int64  `json:"size_bytes"`
	ModTime   int64  `json:"mod_time"`
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
	limit, err := entryPageLimit(r.URL.Query().Get("limit"))
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_limit", "limit 必须为 1 到 500 的整数")
		return
	}
	cursor, err := s.decodeEntryCursor(r.URL.Query().Get("cursor"), id, path)
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_cursor", "目录游标无效")
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
	before, err := file.Stat()
	if err != nil || !before.IsDir() {
		fail(w, 400, "not_directory", "目标不是可读取目录")
		return
	}
	entries := make([]v1.Entry, 0)
	fingerprints := make([]entryFingerprint, 0)
	scanned := 0
	for {
		if r.Context().Err() != nil {
			return
		}
		items, readErr := file.ReadDir(1000)
		if readErr != nil && readErr != io.EOF {
			fail(w, 403, "path_denied", "目录内容不可读取")
			return
		}
		scanned += len(items)
		if scanned > maxDirectoryScanEntries {
			fail(w, http.StatusRequestEntityTooLarge, "directory_too_large", "目录超过 100000 项扫描上限，请拆分子目录")
			return
		}
		for _, item := range items {
			info, err := item.Info()
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					fail(w, http.StatusConflict, "cursor_stale", "目录在读取期间发生变化，请从第一页重新读取")
					return
				}
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
			fingerprints = append(fingerprints, entryFingerprint{Name: item.Name(), Directory: info.IsDir(), SizeBytes: size, ModTime: info.ModTime().UnixNano()})
		}
		if readErr == io.EOF {
			break
		}
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		fail(w, http.StatusConflict, "cursor_stale", "目录在读取期间发生变化，请从第一页重新读取")
		return
	}
	slices.SortFunc(entries, func(a, b v1.Entry) int { return strings.Compare(a.Name, b.Name) })
	slices.SortFunc(fingerprints, func(a, b entryFingerprint) int { return strings.Compare(a.Name, b.Name) })
	// Metadata fingerprint, not a file-content hash or an atomic filesystem
	// snapshot. Filesystem changes during enumeration may still race a page;
	// callers must use immutable manifests for content identity.
	snapshotData, err := json.Marshal(struct {
		DirectoryModTime int64              `json:"directory_mod_time"`
		Items            []entryFingerprint `json:"items"`
	}{before.ModTime().UnixNano(), fingerprints})
	if err != nil {
		fail(w, http.StatusInternalServerError, "snapshot_failed", "目录快照生成失败")
		return
	}
	snapshot := sha256.Sum256(snapshotData)
	snapshotHex := hex.EncodeToString(snapshot[:])
	if cursor != nil && cursor.Snapshot != snapshotHex {
		fail(w, http.StatusConflict, "cursor_stale", "目录在分页期间发生变化，请从第一页重新读取")
		return
	}
	start := 0
	if cursor != nil {
		start = sort.Search(len(entries), func(i int) bool { return entries[i].Name > cursor.Last })
	}
	end := start + limit
	if end > len(entries) {
		end = len(entries)
	}
	page := entries[start:end]
	hasMore := end < len(entries)
	nextCursor := ""
	if hasMore {
		nextCursor, err = s.encodeEntryCursor(entryCursor{Version: 1, Scope: entryCursorScope(id, path), Snapshot: snapshotHex, Last: page[len(page)-1].Name})
		if err != nil {
			fail(w, http.StatusInternalServerError, "cursor_failed", "目录游标生成失败")
			return
		}
	}
	send(w, 200, v1.EntryList{ShareID: id, Path: path, Entries: page, NextCursor: nextCursor, HasMore: hasMore})
}

func entryPageLimit(raw string) (int, error) {
	if raw == "" {
		return v1.DefaultEntryPageSize, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > v1.MaxEntryPageSize || strconv.Itoa(limit) != raw {
		return 0, errors.New("invalid limit")
	}
	return limit, nil
}

func entryCursorScope(shareID, path string) string {
	data, _ := json.Marshal([]string{shareID, path})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (s *Server) encodeEntryCursor(cursor entryCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, s.entryCursorKey[:])
	_, _ = mac.Write(data)
	return base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *Server) decodeEntryCursor(raw, shareID, path string) (*entryCursor, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > maxEntryCursorBytes {
		return nil, errors.New("cursor too large")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return nil, errors.New("cursor format invalid")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	mac := hmac.New(sha256.New, s.entryCursorKey[:])
	_, _ = mac.Write(data)
	if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, errors.New("cursor signature invalid")
	}
	var cursor entryCursor
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil || cursor.Version != 1 || cursor.Scope != entryCursorScope(shareID, path) || cursor.Last == "" || len(cursor.Snapshot) != sha256.Size*2 {
		return nil, errors.New("cursor fields invalid")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("cursor trailing data")
	}
	if _, err := hex.DecodeString(cursor.Snapshot); err != nil {
		return nil, err
	}
	return &cursor, nil
}
