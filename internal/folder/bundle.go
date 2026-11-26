// Package folder provides a deterministic directory bundle, safe extraction and
// atomic no-overwrite publication. Bytes still use the existing block engine.
package folder

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"github.com/DVE-FCT/HopSend/internal/manifest"
)

const MaxEntries = 10000
const maxIndexBytes = 16 << 20
const indexName = ".hopsend-folder-index.json"

type Entry struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256,omitempty"`
	stamp     int64
}
type Index struct {
	Version int     `json:"version"`
	Name    string  `json:"name"`
	Entries []Entry `json:"entries"`
}

func SafePath(path string) error {
	if !fs.ValidPath(path) || path == "." || strings.Contains(path, `\`) {
		return errors.New("目录包路径必须为安全相对路径")
	}
	if len(path) > 4096 {
		return errors.New("相对路径过长")
	}
	for _, part := range strings.Split(path, "/") {
		if err := filesystem.SafeLeaf(part); err != nil {
			return err
		}
		if len([]rune(part)) > 200 {
			return errors.New("路径单级名称过长")
		}
	}
	if strings.EqualFold(path, indexName) {
		return errors.New("目录中含有协议保留名称")
	}
	return nil
}

func validate(index Index) error {
	if index.Version != 1 || filesystem.SafeLeaf(index.Name) != nil || index.Entries == nil || len(index.Entries) > MaxEntries {
		return errors.New("目录索引格式或数量无效")
	}
	seen := map[string]bool{}
	for _, entry := range index.Entries {
		if err := SafePath(entry.Path); err != nil {
			return err
		}
		key := strings.ToLower(entry.Path)
		if _, ok := seen[key]; ok {
			return errors.New("路径重复或大小写冲突")
		}
		parent := filepath.ToSlash(filepath.Dir(entry.Path))
		if parent != "." && !seen[strings.ToLower(parent)] {
			return errors.New("目录索引缺少前置父目录")
		}
		if entry.Bytes < 0 || (entry.Directory && (entry.Bytes != 0 || entry.SHA256 != "")) || (!entry.Directory && !manifest.ValidID(entry.SHA256)) {
			return errors.New("目录索引字节或哈希无效")
		}
		seen[key] = entry.Directory
	}
	return nil
}

func scan(root *os.Root) (Index, error) {
	index := Index{Version: 1, Name: filepath.Base(root.Name()), Entries: []Entry{}}
	err := fs.WalkDir(root.FS(), ".", func(path string, item fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		if err = SafePath(path); err != nil {
			return err
		}
		info, err := root.Lstat(path)
		if err != nil {
			return err
		}
		if filesystem.IsReparse(info) || (!info.Mode().IsRegular() && !info.IsDir()) {
			return errors.New("目录包含链接、重解析点或非常规文件")
		}
		index.Entries = append(index.Entries, Entry{Path: path, Directory: info.IsDir(), Bytes: info.Size(), stamp: info.ModTime().UnixNano()})
		if info.IsDir() {
			index.Entries[len(index.Entries)-1].Bytes = 0
			index.Entries[len(index.Entries)-1].stamp = 0
		}
		if len(index.Entries) > MaxEntries {
			return errors.New("目录超过 10000 项上限")
		}
		return nil
	})
	return index, err
}

// Create scans names first and reopens every file through os.Root. A second scan
// rejects structural/size changes; streaming hashes bind each archived file.
func Create(ctx context.Context, source, output string) (index Index, returnedErr error) {
	info, err := os.Lstat(source)
	if err != nil || !info.IsDir() || filesystem.IsReparse(info) {
		return index, errors.New("源文件夹不可用或为链接")
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		return index, err
	}
	defer root.Close()
	return CreateRoot(ctx, root, output)
}

// CreateRoot packages an already opened root without resolving its original
// pathname again. The caller retains ownership of root.
func CreateRoot(ctx context.Context, root *os.Root, output string) (index Index, returnedErr error) {
	index, err := scan(root)
	if err != nil {
		return index, err
	}
	f, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return index, err
	}
	defer func() {
		_ = f.Close()
		if returnedErr != nil {
			_ = os.Remove(output)
		}
	}()
	w := tar.NewWriter(f)
	for i := range index.Entries {
		e := &index.Entries[i]
		if err = ctx.Err(); err != nil {
			return index, err
		}
		h := &tar.Header{Name: e.Path, Mode: 0600, Size: e.Bytes, Typeflag: tar.TypeReg, Format: tar.FormatPAX}
		if e.Directory {
			h.Name += "/"
			h.Mode = 0700
			h.Typeflag = tar.TypeDir
		}
		if err = w.WriteHeader(h); err != nil {
			return index, err
		}
		if e.Directory {
			continue
		}
		input, err := root.Open(e.Path)
		if err != nil {
			return index, err
		}
		before, err := input.Stat()
		if err != nil {
			_ = input.Close()
			return index, err
		}
		if before.Size() != e.Bytes || before.ModTime().UnixNano() != e.stamp {
			_ = input.Close()
			return index, errors.New("源文件在目录扫描后改变")
		}
		hash := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(w, hash), contextReader{ctx: ctx, reader: io.LimitReader(input, e.Bytes)})
		after, statErr := input.Stat()
		_ = input.Close()
		if copyErr != nil || statErr != nil || n != e.Bytes || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
			return index, errors.New("生成目录版本时源文件改变或读取失败")
		}
		e.SHA256 = hex.EncodeToString(hash.Sum(nil))
	}
	second, err := scan(root)
	if err != nil || len(second.Entries) != len(index.Entries) {
		return index, errors.New("生成目录版本时目录结构改变")
	}
	for i, e := range second.Entries {
		if e.Path != index.Entries[i].Path || e.Directory != index.Entries[i].Directory || e.Bytes != index.Entries[i].Bytes || e.stamp != index.Entries[i].stamp {
			return index, errors.New("目录结构或文件内容改变")
		}
	}
	if err = validate(index); err != nil {
		return index, err
	}
	b, err := json.Marshal(index)
	if err != nil || len(b) > maxIndexBytes {
		return index, errors.New("目录索引过大")
	}
	if err = w.WriteHeader(&tar.Header{Name: indexName, Mode: 0600, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
		return index, err
	}
	if _, err = w.Write(b); err != nil {
		return index, err
	}
	if err = w.Close(); err != nil {
		return index, err
	}
	if err = f.Sync(); err != nil {
		return index, err
	}
	return index, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func readArchive(ctx context.Context, archive string, root *os.Root, write bool) (Index, error) {
	var index Index
	f, err := os.Open(archive)
	if err != nil {
		return index, err
	}
	defer f.Close()
	reader := tar.NewReader(f)
	actual := []Entry{}
	seen := map[string]bool{}
	indexSeen := false
	for {
		h, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return index, err
		}
		if ctx.Err() != nil {
			return index, ctx.Err()
		}
		if h.Name == indexName {
			if indexSeen || h.Typeflag != tar.TypeReg || h.Size > maxIndexBytes {
				return index, errors.New("目录索引重复或过大")
			}
			b, err := io.ReadAll(io.LimitReader(reader, maxIndexBytes+1))
			if err != nil {
				return index, err
			}
			decoder := json.NewDecoder(strings.NewReader(string(b)))
			decoder.DisallowUnknownFields()
			if err = decoder.Decode(&index); err != nil {
				return index, err
			}
			if err = decoder.Decode(new(any)); err != io.EOF {
				return index, errors.New("目录索引有额外内容")
			}
			indexSeen = true
			continue
		}
		if indexSeen {
			return index, errors.New("目录索引必须位于包末尾")
		}
		path := strings.TrimSuffix(h.Name, "/")
		if err = SafePath(path); err != nil {
			return index, err
		}
		key := strings.ToLower(path)
		if _, ok := seen[key]; ok {
			return index, errors.New("包内路径重复或大小写冲突")
		}
		parent := filepath.ToSlash(filepath.Dir(path))
		if parent != "." && !seen[strings.ToLower(parent)] {
			return index, errors.New("包内缺少父目录")
		}
		e := Entry{Path: path, Directory: h.Typeflag == tar.TypeDir, Bytes: h.Size}
		if e.Directory {
			if h.Size != 0 {
				return index, errors.New("目录项不应含数据")
			}
			if write {
				if err = root.Mkdir(path, 0700); err != nil {
					return index, err
				}
			}
		} else {
			if h.Typeflag != tar.TypeReg || h.Size < 0 {
				return index, errors.New("目录包禁止链接、设备和非常规条目")
			}
			hash := sha256.New()
			var output io.Writer = hash
			var file *os.File
			if write {
				file, err = root.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if err != nil {
					return index, err
				}
				output = io.MultiWriter(file, hash)
			}
			n, copyErr := io.Copy(output, contextReader{ctx: ctx, reader: reader})
			if file != nil {
				syncErr := file.Sync()
				closeErr := file.Close()
				if copyErr == nil {
					copyErr = errors.Join(syncErr, closeErr)
				}
			}
			if copyErr != nil || n != h.Size {
				return index, errors.New("目录包读取、写入或同步失败")
			}
			e.SHA256 = hex.EncodeToString(hash.Sum(nil))
		}
		actual = append(actual, e)
		seen[key] = e.Directory
		if len(actual) > MaxEntries {
			return index, errors.New("目录包条目过多")
		}
	}
	if !indexSeen {
		return index, errors.New("目录包缺少索引")
	}
	if err = validate(index); err != nil {
		return index, err
	}
	if len(actual) != len(index.Entries) {
		return index, errors.New("索引条目数量与实际包不一致")
	}
	for i, e := range actual {
		if e != index.Entries[i] {
			return index, errors.New("包内路径、长度或 SHA-256 与索引不一致")
		}
	}
	return index, nil
}

func Verify(ctx context.Context, archive, directory string) error {
	index, err := readArchive(ctx, archive, nil, false)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	actual, err := scan(root)
	if err != nil {
		return err
	}
	if len(actual.Entries) != len(index.Entries) {
		return errors.New("目标目录条目数量变化")
	}
	sort.Slice(actual.Entries, func(i, j int) bool { return actual.Entries[i].Path < actual.Entries[j].Path })
	expected := append([]Entry{}, index.Entries...)
	sort.Slice(expected, func(i, j int) bool { return expected[i].Path < expected[j].Path })
	for i, e := range actual.Entries {
		want := expected[i]
		if e.Path != want.Path || e.Directory != want.Directory || e.Bytes != want.Bytes {
			return errors.New("目标目录路径或尺寸变化")
		}
		if !e.Directory {
			f, err := root.Open(e.Path)
			if err != nil {
				return err
			}
			digest, err := manifest.HashReader(ctx, f)
			_ = f.Close()
			if err != nil || digest != want.SHA256 {
				return errors.New("目标目录文件 SHA-256 不一致")
			}
		}
	}
	return nil
}

func VerifyContent(ctx context.Context, archive, directory, expected string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	digest, err := manifest.HashReader(ctx, f)
	_ = f.Close()
	if err != nil {
		return err
	}
	if digest != expected {
		return errors.New("保留目录包的完整 SHA-256 改变")
	}
	return Verify(ctx, archive, directory)
}

// Publish retains the verified archive for durable reconciliation and builds a
// complete sibling directory before a same-volume no-overwrite rename.
func Publish(ctx context.Context, archive, destination string) error {
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return errors.New("目标目录已存在或不可访问，不会合并覆盖")
	}
	staging := archive + ".folder"
	if info, err := os.Lstat(staging); err == nil {
		if !info.IsDir() || filesystem.IsReparse(info) {
			return errors.New("目录展开暂存类型错误")
		}
		if err = Verify(ctx, archive, staging); err != nil {
			parent, openErr := os.OpenRoot(filepath.Dir(staging))
			if openErr != nil {
				return openErr
			}
			err = parent.RemoveAll(filepath.Base(staging))
			_ = parent.Close()
			if err != nil {
				return err
			}
		} else {
			return filesystem.PublishNoReplace(staging, destination)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Mkdir(staging, 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(staging)
	if err != nil {
		return err
	}
	_, err = readArchive(ctx, archive, root, true)
	_ = root.Close()
	if err != nil {
		return err
	}
	return filesystem.PublishNoReplace(staging, destination)
}
