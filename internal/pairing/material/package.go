package material

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/DVE-FCT/HopSend/contracts/pairing/v1"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"golang.org/x/crypto/ssh"
)

type PackagePreview struct {
	Device        v1.ReplyDevice `json:"device"`
	PackageSHA256 string         `json:"package_sha256"`
	TrustVerified bool           `json:"trust_verified"`
}

// PreviewPackage checks structure and self-consistency only. Its fingerprints
// are untrusted display data until the owner compares them with the source.
// This result must never substitute for independent expectations in ImportReply.
func PreviewPackage(data []byte) (PackagePreview, error) {
	var preview PackagePreview
	if len(data) > v1.MaxReplyBytes {
		return preview, failure("预览配对包", "reply_too_large", "配对包超过 256 KiB")
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(reader.File) != 3 {
		return preview, failure("预览配对包", "invalid_archive", "需要包含三项资料的配对包")
	}
	var device v1.ReplyDevice
	found := false
	for _, entry := range reader.File {
		if entry.Name != "device.json" {
			continue
		}
		if found || entry.UncompressedSize64 > v1.MaxJSONBytes {
			return preview, failure("预览配对包", "invalid_archive", "设备资料重复或过大")
		}
		found = true
		stream, err := entry.Open()
		if err != nil {
			return preview, failure("预览配对包", "invalid_archive", "设备资料无法读取")
		}
		body, readErr := io.ReadAll(io.LimitReader(stream, v1.MaxJSONBytes+1))
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil || strictJSON(body, &device) != nil {
			return preview, failure("预览配对包", "invalid_device", "设备资料无效")
		}
	}
	if !found {
		return preview, failure("预览配对包", "invalid_device", "缺少设备资料")
	}
	// Self-reported pins are used only for structural validation here, not trust.
	verified, err := VerifyReply(data, Expected{SSHFingerprint: device.HostFingerprint, CASHA256: device.CASHA256})
	if err != nil {
		return preview, err
	}
	return PackagePreview{Device: verified.Device, PackageSHA256: digest(data), TrustVerified: false}, nil
}

func ReadPackage(path string) ([]byte, error) {
	data, err := readRegular(path, v1.MaxReplyBytes)
	if err != nil {
		return nil, failure("读取配对包", "reply_unavailable", "配对包不是可读取的有界常规文件")
	}
	return data, nil
}

func ParseRequestFile(path string) (v1.Request, error) {
	data, err := readRegular(path, v1.MaxJSONBytes)
	if err != nil {
		return v1.Request{}, failure("读取请求", "request_unavailable", "公开请求不可读取或超过大小上限")
	}
	return ParseRequest(data)
}

// LocalRequest verifies binding without writing files or granting any trust.
func ExportRequest(trustDirectory, id, receiverIP, destination string) (RequestResult, error) {
	request, err := LocalRequest(trustDirectory, id, receiverIP)
	if err != nil {
		return RequestResult{}, err
	}
	path, err := storePath(destination)
	if err != nil {
		return RequestResult{}, failure("导出请求", "invalid_output", "公开请求输出路径无效")
	}
	if existing, err := ParseRequestFile(path); err == nil && existing == request {
		return RequestResult{Request: request, RequestFile: path}, nil
	}
	data, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return RequestResult{}, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".public-request-*.tmp")
	if err != nil {
		return RequestResult{}, failure("导出请求", "write_failed", "无法写入公开请求")
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	_, err = file.Write(append(data, '\n'))
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return RequestResult{}, failure("导出请求", "write_failed", "公开请求写入失败")
	}
	if err := filesystem.PublishNoReplace(temporary, path); err != nil {
		return RequestResult{}, failure("导出请求", "publish_failed", "不覆盖已有不同的公开请求")
	}
	return RequestResult{Request: request, RequestFile: path}, nil
}

func LocalRequest(trustDirectory, id, receiverIP string) (v1.Request, error) {
	var request v1.Request
	if !requestIDPattern.MatchString(id) || (receiverIP != "" && !tailIP(receiverIP)) {
		return request, failure("绑定本机请求", "binding_mismatch", "请求标识或目标地址无效")
	}
	base, err := storePath(trustDirectory)
	if err != nil {
		return request, failure("绑定本机请求", "invalid_store", "信任目录无效或经过链接")
	}
	dir := filepath.Join(base, id)
	request, err = ParseRequestFile(filepath.Join(dir, "request.json"))
	if err != nil {
		return request, err
	}
	if request.RequestID != id || request.SourceIP == receiverIP {
		return request, failure("绑定本机请求", "binding_mismatch", "回应不属于本机原请求或目标是本机")
	}
	private, err := readRegular(filepath.Join(dir, "id_ed25519"), v1.MaxJSONBytes)
	if err != nil {
		return request, failure("核对本机密钥", "private_key_unavailable", "原请求的私钥不可读取")
	}
	signer, err := ssh.ParsePrivateKey(private)
	if err != nil || strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) != request.PublicKey {
		return request, failure("核对本机密钥", "private_key_mismatch", "本机私钥与原请求不匹配")
	}
	return request, nil
}

// ExportPackage rebuilds only the three allowed entries, removing archive
// comments and unrelated trailing payload. The output never includes a key.
func ExportPackage(ctx context.Context, data []byte, expected Expected, destination string) (PackagePreview, error) {
	var preview PackagePreview
	verified, err := VerifyReply(data, expected)
	if err != nil {
		return preview, err
	}
	if err := ctx.Err(); err != nil {
		return preview, err
	}
	path, err := storePath(destination)
	if err != nil || !strings.EqualFold(filepath.Ext(path), ".hopsend-pair") {
		return preview, failure("导出配对包", "invalid_output", "请选择新的 .hopsend-pair 文件名")
	}
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for _, name := range []string{"ca.crt", "client.token", "device.json"} {
		entry, err := archive.Create(name)
		if err != nil {
			return preview, failure("导出配对包", "write_failed", "配对包编码失败")
		}
		if _, err := entry.Write(verified.files[name]); err != nil {
			return preview, failure("导出配对包", "write_failed", "配对包编码失败")
		}
	}
	if err := archive.Close(); err != nil || buffer.Len() > v1.MaxReplyBytes {
		return preview, failure("导出配对包", "write_failed", "配对包编码失败或超过大小上限")
	}
	nonce, err := randomID()
	if err != nil {
		return preview, failure("导出配对包", "random_unavailable", "安全随机数不可用")
	}
	temporary := filepath.Join(filepath.Dir(path), ".hopsend-pair-"+nonce)
	if err := privateDirectory(temporary); err != nil {
		return preview, failure("导出配对包", "write_failed", "无法创建私密配对包暂存目录")
	}
	defer os.Remove(temporary)
	root, err := os.OpenRoot(temporary)
	if err != nil {
		return preview, failure("导出配对包", "write_failed", "无法打开配对包暂存目录")
	}
	defer root.Close()
	defer root.Remove("reply.tmp")
	if err := writeNew(root, "reply.tmp", buffer.Bytes()); err != nil {
		return preview, failure("导出配对包", "write_failed", "配对包写入失败")
	}
	if err := ctx.Err(); err != nil {
		return preview, err
	}
	if err := filesystem.PublishNoReplace(filepath.Join(temporary, "reply.tmp"), path); err != nil {
		return preview, failure("导出配对包", "publish_failed", "无法发布配对包，不覆盖已有文件")
	}
	return PackagePreview{Device: verified.Device, PackageSHA256: digest(buffer.Bytes()), TrustVerified: false}, nil
}
