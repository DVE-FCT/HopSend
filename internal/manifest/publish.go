package manifest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	v1 "github.com/DVE-FCT/HopSend/contracts/manifest/v1"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
)

// Publish copies a regular source into a content-addressed snapshot. The source
// is never hard-linked, so later source edits cannot alter published bytes.
func Publish(ctx context.Context, source, store, share string, chunkSize int64, payload ...string) (v1.Manifest, error) {
	var m v1.Manifest
	if len(payload) > 1 || (len(payload) == 1 && payload[0] != "folder") {
		return m, errors.New("发布内容类型无效")
	}
	if chunkSize < MinChunkSize || chunkSize > MaxChunkSize || !sharePattern.MatchString(share) {
		return m, errors.New("发布分块或共享参数无效")
	}
	name := filepath.Base(source)
	if err := filesystem.SafeLeaf(name); err != nil {
		return m, err
	}
	input, err := os.Open(source)
	if err != nil {
		return m, err
	}
	defer input.Close()
	return PublishFile(ctx, input, name, store, share, chunkSize, payload...)
}

// PublishFile snapshots an already opened, root-constrained source. The caller
// retains ownership of input; its offset must be zero.
func PublishFile(ctx context.Context, input *os.File, name, store, share string, chunkSize int64, payload ...string) (v1.Manifest, error) {
	var m v1.Manifest
	if len(payload) > 1 || (len(payload) == 1 && payload[0] != "folder") || chunkSize < MinChunkSize || chunkSize > MaxChunkSize || !sharePattern.MatchString(share) {
		return m, errors.New("发布参数无效")
	}
	if err := filesystem.SafeLeaf(name); err != nil {
		return m, err
	}
	before, err := input.Stat()
	if err != nil {
		return m, err
	}
	if !before.Mode().IsRegular() {
		return m, errors.New("只允许发布常规文件")
	}
	count := before.Size() / chunkSize
	if before.Size()%chunkSize != 0 {
		count++
	}
	if count > MaxChunks {
		return m, errors.New("文件超过当前分块数量上限")
	}
	objects := filepath.Join(store, "objects")
	manifests := filepath.Join(store, "manifests")
	if err = os.MkdirAll(objects, 0700); err != nil {
		return m, err
	}
	if err = os.MkdirAll(manifests, 0700); err != nil {
		return m, err
	}
	output, err := os.CreateTemp(objects, ".publishing-*")
	if err != nil {
		return m, err
	}
	staging := output.Name()
	defer func() { output.Close(); os.Chmod(staging, 0600); os.Remove(staging) }()
	m = v1.Manifest{SchemaVersion: 1, ShareID: share, Name: name, SizeBytes: before.Size(), ChunkSizeBytes: chunkSize, Chunks: make([]v1.Chunk, 0, count)}
	if len(payload) == 1 {
		m.PayloadKind = payload[0]
	}
	whole := sha256.New()
	buffer := make([]byte, 1<<20)
	for offset := int64(0); offset < m.SizeBytes; {
		length := min(chunkSize, m.SizeBytes-offset)
		block := sha256.New()
		remaining := length
		for remaining > 0 {
			if err = ctx.Err(); err != nil {
				return m, err
			}
			want := min(int64(len(buffer)), remaining)
			n, readErr := io.ReadFull(input, buffer[:want])
			if readErr != nil {
				return m, fmt.Errorf("源文件变化或读取失败: %w", readErr)
			}
			if _, err = output.Write(buffer[:n]); err != nil {
				return m, err
			}
			_, _ = whole.Write(buffer[:n])
			_, _ = block.Write(buffer[:n])
			remaining -= int64(n)
		}
		m.Chunks = append(m.Chunks, v1.Chunk{Index: len(m.Chunks), Offset: offset, Length: length, SHA256: hex.EncodeToString(block.Sum(nil))})
		offset += length
	}
	after, err := input.Stat()
	if err != nil {
		return m, err
	}
	if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return m, errors.New("源文件在发布过程中改变，请重新发布新版本")
	}
	if err = ctx.Err(); err != nil {
		return m, err
	}
	m.ContentID = hex.EncodeToString(whole.Sum(nil))
	m.ManifestID = ID(m)
	if err = Validate(m); err != nil {
		return m, err
	}
	if err = output.Sync(); err != nil {
		return m, err
	}
	if err = output.Close(); err != nil {
		return m, err
	}
	final := filepath.Join(objects, m.ContentID)
	if err = filesystem.PublishNoReplace(staging, final); err != nil {
		existing, openErr := os.Open(final)
		if openErr != nil {
			return m, err
		}
		hash, hashErr := HashReader(ctx, existing)
		existing.Close()
		if hashErr != nil || hash != m.ContentID {
			return m, errors.New("已发布内容损坏，拒绝覆盖或复用")
		}
	}
	if err = os.Chmod(final, 0444); err != nil {
		return m, err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return m, err
	}
	meta, err := os.CreateTemp(manifests, ".publishing-*")
	if err != nil {
		return m, err
	}
	defer func() { meta.Close(); os.Remove(meta.Name()) }()
	if _, err = meta.Write(append(data, '\n')); err != nil {
		return m, err
	}
	if err = meta.Sync(); err != nil {
		return m, err
	}
	if err = meta.Close(); err != nil {
		return m, err
	}
	if err = filesystem.PublishNoReplace(meta.Name(), filepath.Join(manifests, m.ManifestID+".json")); err != nil {
		existing, openErr := os.Open(filepath.Join(manifests, m.ManifestID+".json"))
		if openErr != nil {
			return m, err
		}
		saved, decodeErr := Decode(existing)
		existing.Close()
		if decodeErr != nil || saved.ManifestID != m.ManifestID {
			return m, errors.New("已发布清单损坏，拒绝复用")
		}
	}
	return m, nil
}
