// Package material handles pairing data without invoking shells, network
// services or desktop APIs. Operating-system permissions have native adapters.
package material

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"reflect"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	v1 "github.com/DVE-FCT/HopSend/contracts/pairing/v1"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"golang.org/x/crypto/ssh"
)

var (
	requestIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
	userPattern      = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,32}$`)
	sharePattern     = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
	shaPattern       = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
	tailnetPrefix    = netip.MustParsePrefix("100.64.0.0/10")
)

type Error struct {
	Stage   string `json:"stage"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Stage + "：" + e.Message }
func failure(stage, code, message string) error {
	return &Error{Stage: stage, Code: code, Message: message}
}

func tailIP(value string) bool {
	ip, err := netip.ParseAddr(value)
	return err == nil && ip.Is4() && ip.String() == value && tailnetPrefix.Contains(ip)
}

func displayName(value string) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= 128 && strings.IndexFunc(value, unicode.IsControl) < 0
}

func publicKey(value string) (ssh.PublicKey, error) {
	key, comment, options, rest, err := ssh.ParseAuthorizedKey([]byte(value))
	if err != nil || comment != "" || len(options) != 0 || len(bytes.TrimSpace(rest)) != 0 || key.Type() != ssh.KeyAlgoED25519 || strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))) != value {
		return nil, failure("校验公钥", "invalid_public_key", "需要完整且规范的 Ed25519 公钥")
	}
	return key, nil
}

// strictJSON also rejects duplicate top-level keys. The material DTOs are flat;
// DisallowUnknownFields alone would accept duplicate keys with last-value wins.
func strictJSON(data []byte, value any) error {
	if len(data) > v1.MaxJSONBytes || !utf8.Valid(data) {
		return errors.New("JSON 大小或 UTF-8 编码无效")
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return errors.New("需要单一 JSON 对象")
	}
	seen := map[string]bool{}
	allowed := map[string]bool{}
	fields := reflect.TypeOf(value).Elem()
	for i := 0; i < fields.NumField(); i++ {
		allowed[strings.Split(fields.Field(i).Tag.Get("json"), ",")[0]] = true
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return errors.New("JSON 键无效")
		}
		name, ok := token.(string)
		if !ok || seen[name] {
			return errors.New("JSON 包含重复键")
		}
		if !allowed[name] {
			return errors.New("JSON 包含未知字段或字段大小写无效")
		}
		seen[name] = true
		var item json.RawMessage
		if err := decoder.Decode(&item); err != nil {
			return errors.New("JSON 值无效")
		}
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
		return errors.New("JSON 对象未结束")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("JSON 包含额外内容")
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errors.New("JSON 字段或字段类型无效")
	}
	return nil
}

func ParseRequest(data []byte) (v1.Request, error) {
	var request v1.Request
	if err := strictJSON(data, &request); err != nil {
		return request, failure("读取请求", "invalid_request", "请求 JSON 无效、包含未知字段或重复键")
	}
	if request.SchemaVersion != 1 || request.Kind != "hopsend-pair-request" || !requestIDPattern.MatchString(request.RequestID) || !tailIP(request.SourceIP) || !displayName(request.Name) {
		return request, failure("校验请求", "invalid_request", "请求版本、标识、名称或 Tailscale 地址无效")
	}
	_, err := publicKey(request.PublicKey)
	return request, err
}

func windowsConfigPath(value string) bool {
	if len(value) < 4 || !((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) || value[1] != ':' || (value[2] != '/' && value[2] != '\\') || len(value) > 4096 {
		return false
	}
	for _, part := range strings.Split(strings.ReplaceAll(value[3:], `\`, "/"), "/") {
		if filesystem.SafeLeaf(part) != nil {
			return false
		}
	}
	return true
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
