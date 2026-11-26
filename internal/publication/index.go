// Package publication binds explicitly published snapshots to shared paths.
package publication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"github.com/DVE-FCT/HopSend/internal/folder"
	"github.com/DVE-FCT/HopSend/internal/manifest"
)

var (
	ErrUnpublished = errors.New("此路径尚未显式发布，请在源设备发布后重试")
	ErrStale       = errors.New("路径索引或已发布内容不可用，请在源设备重新发布")
	ErrBusy        = errors.New("内容库正在发布或无法取得发布锁，请稍后重试")
	shareID        = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
)

type record struct {
	SchemaVersion int            `json:"schema_version"`
	RootIdentity  string         `json:"root_identity"`
	Version       v1.PathVersion `json:"version"`
}

// ValidatePath applies the portable Windows path rules used by folder transfer.
func ValidatePath(value string) error { return folder.SafePath(value) }

func Validate(value v1.PathVersion, share, relative string) error {
	stamp, err := time.Parse(time.RFC3339Nano, value.PublishedAt)
	if err != nil || stamp.IsZero() || !shareID.MatchString(share) || ValidatePath(relative) != nil || value.ShareID != share || value.Path != relative || value.Name != path.Base(relative) || value.SizeBytes < 0 || !manifest.ValidID(value.ManifestID) || !manifest.ValidID(value.ContentID) || (value.PayloadKind != "file" && value.PayloadKind != "folder") {
		return ErrStale
	}
	return nil
}

func recordPath(share, relative string) string {
	data, _ := json.Marshal([]string{share, relative})
	digest := sha256.Sum256(data)
	return "index/" + hex.EncodeToString(digest[:]) + ".json"
}

func rootIdentity(root *os.Root) (string, error) {
	file, err := root.Open(".")
	if err != nil {
		return "", err
	}
	defer file.Close()
	return filesystem.Identity(file)
}

// CheckPath rejects existing link/reparse components. os.Root additionally
// prevents traversal outside the share if components are concurrently replaced.
func CheckPath(root *os.Root, relative string) error {
	if err := ValidatePath(relative); err != nil {
		return err
	}
	prefix := ""
	for _, part := range strings.Split(relative, "/") {
		prefix = path.Join(prefix, part)
		info, err := root.Lstat(prefix)
		if err != nil {
			return err
		}
		if filesystem.IsReparse(info) {
			return errors.New("路径包含链接或重解析点")
		}
	}
	return nil
}

// Load reads small metadata only. It returns a previously published snapshot,
// even when the live source has since changed or disappeared.
func Load(content, shared *os.Root, share, relative string) (v1.PathVersion, error) {
	var zero v1.PathVersion
	if !shareID.MatchString(share) || ValidatePath(relative) != nil {
		return zero, ErrStale
	}
	metadata := recordPath(share, relative)
	if err := CheckPath(content, metadata); err != nil {
		if os.IsNotExist(err) {
			return zero, ErrUnpublished
		}
		return zero, ErrStale
	}
	file, err := content.Open(metadata)
	if os.IsNotExist(err) {
		return zero, ErrUnpublished
	}
	if err != nil {
		return zero, ErrStale
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<10 {
		return zero, ErrStale
	}
	var saved record
	decoder := json.NewDecoder(io.LimitReader(file, (16<<10)+1))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&saved) != nil || decoder.Decode(new(any)) != io.EOF || saved.SchemaVersion != 1 || Validate(saved.Version, share, relative) != nil {
		return zero, ErrStale
	}
	identity, err := rootIdentity(shared)
	if err != nil || identity != saved.RootIdentity {
		return zero, ErrStale
	}
	if CheckPath(content, "manifests/"+saved.Version.ManifestID+".json") != nil || CheckPath(content, "objects/"+saved.Version.ContentID) != nil {
		return zero, ErrStale
	}
	m, err := manifest.Load(content, saved.Version.ManifestID)
	if err != nil || m.ShareID != share || m.ContentID != saved.Version.ContentID || m.Name != saved.Version.Name || m.SizeBytes != saved.Version.SizeBytes || (m.PayloadKind == "folder") != (saved.Version.PayloadKind == "folder") {
		return zero, ErrStale
	}
	info, err = content.Lstat("objects/" + m.ContentID)
	if err != nil || filesystem.IsReparse(info) || !info.Mode().IsRegular() || info.Size() != m.SizeBytes {
		return zero, ErrStale
	}
	return saved.Version, nil
}

// Publish is a local owner action, never an implicit side effect of browsing.
func Publish(ctx context.Context, rootPath, store, share, relative string, chunkSize int64) (v1.PathVersion, error) {
	var zero v1.PathVersion
	if !shareID.MatchString(share) || ValidatePath(relative) != nil || chunkSize < manifest.MinChunkSize || chunkSize > manifest.MaxChunkSize {
		return zero, errors.New("共享、相对路径或分块参数无效")
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if err := filesystem.CheckComponents(rootPath); err != nil {
		return zero, err
	}
	if err := filesystem.CheckComponents(store); err != nil {
		return zero, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return zero, err
	}
	defer root.Close()
	if err = CheckPath(root, relative); err != nil {
		return zero, err
	}
	input, err := root.Open(relative)
	if err != nil {
		return zero, err
	}
	defer input.Close()
	before, err := input.Stat()
	if err != nil {
		return zero, err
	}
	if !before.IsDir() && !before.Mode().IsRegular() {
		return zero, errors.New("仅可发布常规文件或文件夹")
	}
	if before.IsDir() {
		source, e1 := filepath.Abs(filepath.Join(rootPath, filepath.FromSlash(relative)))
		absoluteStore, e2 := filepath.Abs(store)
		inside, e3 := filepath.Rel(source, absoluteStore)
		sameVolume := strings.EqualFold(filepath.VolumeName(source), filepath.VolumeName(absoluteStore))
		if e1 != nil || e2 != nil || (sameVolume && (e3 != nil || (inside != ".." && !strings.HasPrefix(inside, ".."+string(filepath.Separator))))) {
			return zero, errors.New("内容库不得位于待发布文件夹内")
		}
	}
	if err = os.MkdirAll(store, 0700); err != nil {
		return zero, err
	}
	for _, name := range []string{"objects", "manifests", "index", "publication.lock"} {
		if err := filesystem.CheckComponents(filepath.Join(store, name)); err != nil {
			return zero, err
		}
	}
	lock, err := filesystem.Lock(filepath.Join(store, "publication.lock"))
	if err != nil {
		return zero, ErrBusy
	}
	defer lock.Close()
	identity, err := rootIdentity(root)
	if err != nil {
		return zero, err
	}
	name := path.Base(relative)
	kind := "file"
	if before.IsDir() {
		kind = "folder"
		directory, err := root.OpenRoot(relative)
		if err != nil {
			return zero, err
		}
		defer directory.Close()
		actual, err := directory.Stat(".")
		if err != nil || !os.SameFile(before, actual) {
			return zero, errors.New("源文件夹身份已改变")
		}
		staging, err := os.MkdirTemp(store, ".path-packing-*")
		if err != nil {
			return zero, err
		}
		defer os.Remove(staging)
		bundle := filepath.Join(staging, name)
		defer os.Remove(bundle)
		if _, err = folder.CreateRoot(ctx, directory, bundle); err != nil {
			return zero, err
		}
		packed, err := os.Open(bundle)
		if err != nil {
			return zero, err
		}
		_ = input.Close()
		input = packed
	}
	payload := []string{}
	if kind == "folder" {
		payload = append(payload, "folder")
	}
	m, err := manifest.PublishFile(ctx, input, name, store, share, chunkSize, payload...)
	if before.IsDir() {
		_ = input.Close()
	}
	if err != nil {
		return zero, err
	}
	// Detect replacement of the selected entry before publishing the pointer.
	after, err := root.Lstat(relative)
	if err != nil || filesystem.IsReparse(after) || !os.SameFile(before, after) || (!before.IsDir() && (before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()))) {
		return zero, errors.New("源路径在发布期间改变，请重新发布")
	}
	value := v1.PathVersion{ShareID: share, Path: relative, ManifestID: m.ManifestID, ContentID: m.ContentID, Name: m.Name, SizeBytes: m.SizeBytes, PayloadKind: kind, PublishedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	saved := record{SchemaVersion: 1, RootIdentity: identity, Version: value}
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return zero, err
	}
	indexDir := filepath.Join(store, "index")
	if err = os.MkdirAll(indexDir, 0700); err != nil {
		return zero, err
	}
	if err = filesystem.CheckComponents(indexDir); err != nil {
		return zero, err
	}
	meta, err := os.CreateTemp(indexDir, ".publishing-*")
	if err != nil {
		return zero, err
	}
	defer func() { meta.Close(); os.Remove(meta.Name()) }()
	if _, err = meta.Write(append(data, '\n')); err != nil {
		return zero, err
	}
	if err = meta.Sync(); err != nil {
		return zero, err
	}
	if err = meta.Close(); err != nil {
		return zero, err
	}
	if err = ctx.Err(); err != nil {
		return zero, err
	}
	if err = filesystem.Replace(meta.Name(), filepath.Join(store, filepath.FromSlash(recordPath(share, relative)))); err != nil {
		return zero, err
	}
	return value, nil
}
