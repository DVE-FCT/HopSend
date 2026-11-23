package manifest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"

	v1 "github.com/DVE-FCT/HopSend/contracts/manifest/v1"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
)

const DefaultChunkSize int64 = 32 << 20
const MinChunkSize int64 = 64 << 10
const MaxChunkSize int64 = 64 << 20
const MaxChunks = 65536
const MaxJSONBytes = 16 << 20

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var sharePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func ValidID(id string) bool { return digestPattern.MatchString(id) }

func ID(m v1.Manifest) string {
	m.ManifestID = ""
	data, _ := json.Marshal(m)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func Validate(m v1.Manifest) error {
	if m.PayloadKind != "" && m.PayloadKind != "folder" {
		return errors.New("清单内容类型不受支持")
	}
	if m.SchemaVersion != 1 || !ValidID(m.ManifestID) || !ValidID(m.ContentID) || !sharePattern.MatchString(m.ShareID) {
		return errors.New("清单版本或内容标识无效")
	}
	if err := filesystem.SafeLeaf(m.Name); err != nil {
		return err
	}
	if m.ChunkSizeBytes < MinChunkSize || m.ChunkSizeBytes > MaxChunkSize || m.SizeBytes < 0 || m.Chunks == nil || len(m.Chunks) > MaxChunks {
		return errors.New("清单大小或分块规则无效")
	}
	want := m.SizeBytes / m.ChunkSizeBytes
	if m.SizeBytes%m.ChunkSizeBytes != 0 {
		want++
	}
	if want != int64(len(m.Chunks)) {
		return errors.New("分块数量不匹配")
	}
	var offset int64
	for i, c := range m.Chunks {
		length := min(m.ChunkSizeBytes, m.SizeBytes-offset)
		if c.Index != i || c.Offset != offset || c.Length != length || length <= 0 || !ValidID(c.SHA256) {
			return fmt.Errorf("分块 %d 的范围或哈希无效", i)
		}
		offset += length
	}
	if offset != m.SizeBytes || ID(m) != m.ManifestID {
		return errors.New("清单内容或标识不匹配")
	}
	if m.SizeBytes == 0 {
		empty := sha256.Sum256(nil)
		if m.ContentID != hex.EncodeToString(empty[:]) {
			return errors.New("空文件内容哈希无效")
		}
	}
	return nil
}

func Decode(reader io.Reader) (v1.Manifest, error) {
	var m v1.Manifest
	data, err := io.ReadAll(io.LimitReader(reader, MaxJSONBytes+1))
	if err != nil {
		return m, err
	}
	if len(data) > MaxJSONBytes {
		return m, errors.New("清单超过大小上限")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&m); err != nil {
		return m, err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return m, errors.New("清单包含额外 JSON")
	}
	return m, Validate(m)
}

func HashReader(ctx context.Context, r io.Reader) (string, error) {
	h := sha256.New()
	buffer := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := r.Read(buffer)
		if n > 0 {
			_, _ = h.Write(buffer[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func Load(root *os.Root, id string) (v1.Manifest, error) {
	if !ValidID(id) {
		return v1.Manifest{}, errors.New("清单 ID 无效")
	}
	file, err := root.Open("manifests/" + id + ".json")
	if err != nil {
		return v1.Manifest{}, err
	}
	defer file.Close()
	m, err := Decode(file)
	if err == nil && m.ManifestID != id {
		err = errors.New("清单文件与请求版本不一致")
	}
	return m, err
}
