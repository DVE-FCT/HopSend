package material

import (
	"archive/zip"
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	v1 "github.com/DVE-FCT/HopSend/contracts/pairing/v1"
	"golang.org/x/crypto/ssh"
)

type Expected struct {
	SSHFingerprint string
	CASHA256       string
	RequestID      string
	ReceiverIP     string
}

type VerifiedReply struct {
	Device  v1.ReplyDevice
	Address string
	files   map[string][]byte
}

var fingerprintPattern = regexp.MustCompile(`^SHA256:[a-zA-Z0-9+/]{43}$`)

func VerifyReply(data []byte, expected Expected) (*VerifiedReply, error) {
	if !fingerprintPattern.MatchString(expected.SSHFingerprint) || !shaPattern.MatchString(expected.CASHA256) || (expected.RequestID != "" && !requestIDPattern.MatchString(expected.RequestID)) || (expected.ReceiverIP != "" && !tailIP(expected.ReceiverIP)) {
		return nil, failure("核对信任来源", "invalid_expectation", "需要已核对的 SSH 指纹和 CA 哈希，以及有效的可选请求/接收地址绑定")
	}
	if len(data) > v1.MaxReplyBytes {
		return nil, failure("读取回应", "reply_too_large", "回应包超过 256 KiB")
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(reader.File) != 3 {
		return nil, failure("读取回应", "invalid_archive", "回应必须为恰好包含三项资料的 ZIP")
	}
	files := make(map[string][]byte, 3)
	for _, file := range reader.File {
		if (file.Name != "ca.crt" && file.Name != "client.token" && file.Name != "device.json") || files[file.Name] != nil || !file.Mode().IsRegular() || file.Mode()&os.ModeSymlink != 0 || file.UncompressedSize64 > v1.MaxJSONBytes {
			return nil, failure("展开回应", "invalid_archive", "回应含额外、重复、越界、链接或过大项目")
		}
		stream, err := file.Open()
		if err != nil {
			return nil, failure("展开回应", "invalid_archive", "回应项目无法读取")
		}
		value, readErr := io.ReadAll(io.LimitReader(stream, v1.MaxJSONBytes+1))
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil || len(value) > v1.MaxJSONBytes || uint64(len(value)) != file.UncompressedSize64 {
			return nil, failure("展开回应", "invalid_archive", "回应项目损坏或展开超过 64 KiB")
		}
		files[file.Name] = value
	}
	var device v1.ReplyDevice
	if strictJSON(files["device.json"], &device) != nil || device.SchemaVersion != 1 || device.Kind != "hopsend-pair-reply" || !requestIDPattern.MatchString(device.RequestID) || strings.TrimSpace(device.Name) == "" || !displayName(device.Name) {
		return nil, failure("校验回应", "invalid_device", "回应 JSON、版本、标识或设备名称无效")
	}
	address := strings.TrimSuffix(device.SSHAddress, ":22")
	if !tailIP(address) || device.SSHAddress != address+":22" || device.AgentURL != "https://"+address+":9443" || !userPattern.MatchString(device.SSHUser) || !sharePattern.MatchString(device.ShareID) || !windowsConfigPath(device.RemoteAgentConfig) {
		return nil, failure("校验回应", "invalid_device", "回应地址、端口、账号、共享或远端 Windows 配置路径无效")
	}
	if (expected.RequestID != "" && expected.RequestID != device.RequestID) || (expected.ReceiverIP != "" && expected.ReceiverIP != address) {
		return nil, failure("绑定回应", "binding_mismatch", "回应不属于本次请求或目标设备")
	}
	key, err := publicKey(device.HostPublicKey)
	if err != nil {
		return nil, err
	}
	if ssh.FingerprintSHA256(key) != expected.SSHFingerprint || device.HostFingerprint != expected.SSHFingerprint {
		return nil, failure("核对 SSH 身份", "ssh_fingerprint_mismatch", "SSH 主机指纹不匹配")
	}
	caHash := digest(files["ca.crt"])
	if caHash != strings.ToLower(expected.CASHA256) || strings.ToLower(device.CASHA256) != caHash {
		return nil, failure("核对 HTTPS 身份", "ca_fingerprint_mismatch", "CA 公钥指纹不匹配")
	}
	block, rest := pem.Decode(files["ca.crt"])
	if block == nil || !bytes.HasPrefix(bytes.TrimSpace(files["ca.crt"]), []byte("-----BEGIN CERTIFICATE-----")) || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, failure("核对 HTTPS 身份", "invalid_ca", "回应必须包含唯一 CA 公钥证书")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	now := time.Now()
	if err != nil || !certificate.IsCA || !certificate.BasicConstraintsValid || certificate.CheckSignatureFrom(certificate) != nil || now.Before(certificate.NotBefore) || !now.Before(certificate.NotAfter) {
		return nil, failure("核对 HTTPS 身份", "invalid_ca", "CA 证书角色、签名或有效期无效")
	}
	if !shaPattern.MatchString(strings.TrimSpace(string(files["client.token"]))) {
		return nil, failure("校验凭据", "invalid_token", "API 凭据格式无效")
	}
	return &VerifiedReply{Device: device, Address: address, files: files}, nil
}

func VerifyReplyFile(path string, expected Expected) (*VerifiedReply, error) {
	data, err := readRegular(path, v1.MaxReplyBytes)
	if err != nil {
		return nil, failure("读取回应", "reply_unavailable", "回应文件不可读取、不是常规文件或超过大小上限")
	}
	return VerifyReply(data, expected)
}
