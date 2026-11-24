package material

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/DVE-FCT/HopSend/contracts/pairing/v1"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type RequestOptions struct {
	TrustDirectory, OutputDirectory, SourceIP, Name string
}

type RequestResult struct {
	Request     v1.Request `json:"request"`
	RequestFile string     `json:"request_file"`
}

type ImportResult struct {
	RequestID      string `json:"request_id"`
	ConnectionFile string `json:"connection_file"`
	Verification   string `json:"verification"`
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func readRegular(path string, limit int64) ([]byte, error) {
	if err := filesystem.CheckComponents(path); err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || filesystem.IsReparse(before) || before.Size() > limit {
		return nil, errors.New("资料不是有界的常规文件")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() || after.Size() > limit {
		return nil, errors.New("资料在读取前发生变化")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("资料超过大小上限或读取失败")
	}
	return data, nil
}

func writeNew(root *os.Root, name string, data []byte) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return file.Close()
}

func storePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("需要专用的信任资料目录")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := filesystem.CheckComponents(absolute); err != nil {
		return "", err
	}
	return absolute, nil
}

// EnsurePrivateDirectory protects per-account caches before they contain reply
// credentials. It never takes ownership of an existing foreign directory.
func EnsurePrivateDirectory(path string) error {
	absolute, err := storePath(path)
	if err != nil {
		return failure("保护资料", "invalid_store", "资料目录无效或经过链接")
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		return failure("保护资料", "permission_failed", "无法创建资料目录的父目录")
	}
	if err := privateDirectory(absolute); err == nil {
		return nil
	} else if !os.IsExist(err) {
		return failure("保护资料", "permission_failed", "无法创建账号专属资料目录")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || filesystem.IsReparse(info) {
		return failure("保护资料", "invalid_store", "已有资料路径不是受控常规目录")
	}
	if err := protectDirectory(absolute); err != nil {
		return failure("保护资料", "permission_failed", "无法核对资料目录所属账号或设置权限")
	}
	return nil
}

func CreateRequest(ctx context.Context, options RequestOptions) (RequestResult, error) {
	var result RequestResult
	if !tailIP(options.SourceIP) || !displayName(options.Name) {
		return result, failure("生成请求", "invalid_request", "需要规范的 Tailscale IPv4 和有效显示名称")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	base, err := storePath(options.TrustDirectory)
	if err != nil {
		return result, failure("生成请求", "invalid_store", "信任目录无效或经过链接")
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		return result, failure("生成请求", "store_unavailable", "无法创建信任目录")
	}
	id, err := randomID()
	if err != nil {
		return result, failure("生成密钥", "random_unavailable", "安全随机数不可用")
	}
	temporary := ".request-" + id
	if err := privateDirectory(filepath.Join(base, temporary)); err != nil {
		return result, failure("保护资料", "permission_failed", "无法创建账号专属私钥目录")
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return result, failure("生成请求", "store_unavailable", "无法打开信任目录")
	}
	defer root.Close()
	defer root.RemoveAll(temporary)
	directory, err := root.OpenRoot(temporary)
	if err != nil {
		return result, failure("生成请求", "store_unavailable", "无法打开私钥暂存目录")
	}
	defer directory.Close()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return result, failure("生成密钥", "key_generation_failed", "Ed25519 密钥生成失败")
	}
	block, err := ssh.MarshalPrivateKey(private, "HopSend pairing")
	if err != nil {
		return result, failure("生成密钥", "key_generation_failed", "SSH 私钥编码失败")
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		return result, failure("生成密钥", "key_generation_failed", "SSH 公钥编码失败")
	}
	request := v1.Request{SchemaVersion: 1, Kind: "hopsend-pair-request", RequestID: id, SourceIP: options.SourceIP, PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))), Name: options.Name}
	requestData, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return result, failure("生成请求", "invalid_request", "请求编码失败")
	}
	files := []struct {
		name string
		data []byte
	}{
		{"id_ed25519", pem.EncodeToMemory(block)}, {"id_ed25519.pub", ssh.MarshalAuthorizedKey(key)}, {"request.json", append(requestData, '\n')},
	}
	for _, file := range files {
		if err := writeNew(directory, file.name, file.data); err != nil {
			return result, failure("保存请求", "write_failed", "密钥或请求写入失败，尚未发布")
		}
	}
	if err := directory.Close(); err != nil {
		return result, failure("保存请求", "write_failed", "私钥目录关闭失败")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := root.Rename(temporary, id); err != nil {
		return result, failure("保存请求", "publish_failed", "请求目录发布失败，不覆盖已有密钥")
	}
	result = RequestResult{Request: request, RequestFile: filepath.Join(base, id, "request.json")}
	if options.OutputDirectory != "" {
		output, err := storePath(options.OutputDirectory)
		if err != nil {
			return result, failure("导出请求", "invalid_output", "输出目录无效；本机请求和私钥已保留")
		}
		file, err := os.CreateTemp(output, ".request-*.tmp")
		if err != nil {
			return result, failure("导出请求", "write_failed", "输出目录不可用；本机请求和私钥已保留")
		}
		temporary := file.Name()
		defer os.Remove(temporary)
		_, err = file.Write(files[2].data)
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			return result, failure("导出请求", "write_failed", "公开请求写入失败；本机私钥已保留")
		}
		destination := filepath.Join(output, "HopSend-pair-request-"+id+".json")
		if err := filesystem.PublishNoReplace(temporary, destination); err != nil {
			return result, failure("导出请求", "publish_failed", "公开请求发布失败，不覆盖已有文件；本机私钥已保留")
		}
		result.RequestFile = destination
	}
	return result, nil
}

func ImportReply(ctx context.Context, trustDirectory string, data []byte, expected Expected) (ImportResult, error) {
	return importReply(ctx, trustDirectory, data, expected, false)
}

// StageTrustRotation publishes an immutable credential version. It does not
// select it for any device, rewrite paired/, or migrate persisted tasks. The
// caller must atomically commit its device reference only after success.
func StageTrustRotation(ctx context.Context, trustDirectory string, data []byte, expected Expected) (ImportResult, error) {
	if expected.RequestID == "" {
		return ImportResult{}, failure("轮换信任", "binding_mismatch", "轮换必须绑定已有配对请求")
	}
	return importReply(ctx, trustDirectory, data, expected, true)
}

func importReply(ctx context.Context, trustDirectory string, data []byte, expected Expected, rotation bool) (ImportResult, error) {
	var result ImportResult
	verified, err := VerifyReply(data, expected)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	base, err := storePath(trustDirectory)
	if err != nil {
		return result, failure("绑定本机请求", "invalid_store", "信任目录无效或经过链接")
	}
	dir := filepath.Join(base, verified.Device.RequestID)
	requestData, err := readRegular(filepath.Join(dir, "request.json"), v1.MaxJSONBytes)
	if err != nil {
		return result, failure("绑定本机请求", "request_not_found", "未找到本机原配对请求")
	}
	request, err := ParseRequest(requestData)
	if err != nil {
		return result, err
	}
	if request.RequestID != verified.Device.RequestID || request.SourceIP == verified.Address {
		return result, failure("绑定本机请求", "binding_mismatch", "回应与本机原请求不一致，或目标是本机")
	}
	private, err := readRegular(filepath.Join(dir, "id_ed25519"), v1.MaxJSONBytes)
	if err != nil {
		return result, failure("核对本机密钥", "private_key_unavailable", "原请求的私钥不可读取")
	}
	signer, err := ssh.ParsePrivateKey(private)
	if err != nil || strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) != request.PublicKey {
		return result, failure("核对本机密钥", "private_key_mismatch", "本机私钥与原请求的公钥不匹配")
	}
	if err := protectDirectory(dir); err != nil {
		return result, failure("保护资料", "permission_failed", "无法核对所属账号或保护配对目录")
	}
	lock, err := filesystem.Lock(filepath.Join(dir, ".import.lock"))
	if err != nil {
		return result, failure("锁定导入", "import_busy", "同一请求已有导入操作，或无法取得写入锁")
	}
	defer lock.Close()
	target := "paired"
	if rotation {
		originalData, err := readRegular(filepath.Join(dir, "paired", "device.json"), v1.MaxJSONBytes)
		var original v1.ReplyDevice
		if err != nil || strictJSON(originalData, &original) != nil || original.RequestID != request.RequestID {
			return ImportResult{}, failure("轮换信任", "trust_not_found", "未找到该请求的已有信任，请先完成首次配对")
		}
		// Hash semantic device fields plus exact credential bytes, so ZIP/JSON
		// formatting changes do not create a different version on retry.
		identity, err := json.Marshal(struct {
			Device    v1.ReplyDevice
			CA, Token []byte
		}{verified.Device, verified.files["ca.crt"], verified.files["client.token"]})
		if err != nil {
			return ImportResult{}, err
		}
		target = "paired-" + digest(identity)
	}
	paired := filepath.Join(dir, target)
	connection := v1.Connection{Name: verified.Device.Name, AgentURL: verified.Device.AgentURL, SSHAddress: verified.Device.SSHAddress, SSHUser: verified.Device.SSHUser, CAFile: filepath.Join(paired, "ca.crt"), TokenFile: filepath.Join(paired, "client.token"), KeyFile: filepath.Join(dir, "id_ed25519"), KnownHostsFile: filepath.Join(paired, "known_hosts"), ShareID: verified.Device.ShareID, RemoteAgentConfig: verified.Device.RemoteAgentConfig, ManageAgent: true}
	connectionData, err := json.MarshalIndent(connection, "", "  ")
	if err != nil {
		return result, failure("导入资料", "invalid_connection", "连接配置编码失败")
	}
	hostKey, _ := publicKey(verified.Device.HostPublicKey)
	known := []byte(knownhosts.Line([]string{verified.Address}, hostKey) + "\n")
	result = ImportResult{RequestID: request.RequestID, ConnectionFile: filepath.Join(paired, "connection.json"), Verification: "not_run"}
	if info, err := os.Lstat(paired); err == nil {
		if !info.IsDir() || filesystem.IsReparse(info) {
			return ImportResult{}, failure("固定信任", "trust_conflict", "已有信任目录无效，不替换原信任")
		}
		for name, expectedData := range verified.files {
			actual, err := readRegular(filepath.Join(paired, name), v1.MaxJSONBytes)
			matches := err == nil && bytes.Equal(actual, expectedData)
			if name == "device.json" && err == nil {
				var original v1.ReplyDevice
				matches = strictJSON(actual, &original) == nil && original == verified.Device
			}
			if !matches {
				return ImportResult{}, failure("固定信任", "trust_conflict", "重试回应与原信任资料不同，不覆盖原信任")
			}
		}
		actualKnown, err := readRegular(filepath.Join(paired, "known_hosts"), v1.MaxJSONBytes)
		if err != nil || !bytes.Equal(actualKnown, known) {
			return ImportResult{}, failure("固定信任", "trust_conflict", "已有 SSH 主机记录不一致，不覆盖原信任")
		}
		actualData, err := readRegular(result.ConnectionFile, v1.MaxJSONBytes)
		var actual v1.Connection
		if err != nil || strictJSON(actualData, &actual) != nil || actual != connection {
			return ImportResult{}, failure("固定信任", "trust_conflict", "已有连接配置不一致，不覆盖原信任")
		}
		return result, nil
	} else if !os.IsNotExist(err) {
		return ImportResult{}, failure("固定信任", "store_unavailable", "已有信任目录不可检查")
	}
	nonce, err := randomID()
	if err != nil {
		return ImportResult{}, failure("导入资料", "random_unavailable", "安全随机数不可用")
	}
	temporary := ".import-" + nonce
	if err := privateDirectory(filepath.Join(dir, temporary)); err != nil {
		return ImportResult{}, failure("保护资料", "permission_failed", "无法创建账号专属导入目录")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return ImportResult{}, failure("导入资料", "store_unavailable", "配对目录不可打开")
	}
	defer root.Close()
	defer root.RemoveAll(temporary)
	staging, err := root.OpenRoot(temporary)
	if err != nil {
		return ImportResult{}, failure("导入资料", "store_unavailable", "导入暂存目录不可打开")
	}
	defer staging.Close()
	for name, value := range verified.files {
		if err := writeNew(staging, name, value); err != nil {
			return ImportResult{}, failure("导入资料", "write_failed", "信任资料写入失败，尚未发布")
		}
	}
	for name, value := range map[string][]byte{"known_hosts": known, "connection.json": append(connectionData, '\n')} {
		if err := writeNew(staging, name, value); err != nil {
			return ImportResult{}, failure("导入资料", "write_failed", "连接配置写入失败，尚未发布")
		}
	}
	if err := staging.Close(); err != nil {
		return ImportResult{}, failure("导入资料", "write_failed", "暂存目录关闭失败")
	}
	if err := ctx.Err(); err != nil {
		return ImportResult{}, err
	}
	if err := root.Rename(temporary, target); err != nil {
		return ImportResult{}, failure("发布信任", "publish_failed", "完整资料发布失败，不覆盖已有信任")
	}
	return result, nil
}

func ImportReplyFile(ctx context.Context, trustDirectory, replyFile string, expected Expected) (ImportResult, error) {
	data, err := readRegular(replyFile, v1.MaxReplyBytes)
	if err != nil {
		return ImportResult{}, failure("读取回应", "reply_unavailable", "回应文件不可读取、不是常规文件或超过大小上限")
	}
	return ImportReply(ctx, trustDirectory, data, expected)
}
