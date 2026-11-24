package nodeclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	manifestv1 "github.com/DVE-FCT/HopSend/contracts/manifest/v1"
	"github.com/DVE-FCT/HopSend/internal/manifest"
)

type Client struct {
	base  string
	token string
	http  *http.Client
}
type Config struct {
	URL, CACertificate, TokenFile string
	Timeout                       time.Duration
}

type HTTPError struct {
	StatusCode    int
	Code, Message string
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("Agent 返回 HTTP %d：%s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("Agent 返回 HTTP %d", e.StatusCode)
}

func New(cfg Config) (*Client, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("Agent 地址必须为不含凭据、路径或查询的 https://host:port")
	}
	ca, err := os.ReadFile(cfg.CACertificate)
	if err != nil {
		return nil, errors.New("读取 Agent 受信任 CA 失败")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("CA 证书格式无效")
	}
	data, err := os.ReadFile(cfg.TokenFile)
	if err != nil {
		return nil, errors.New("读取 Agent 凭据失败")
	}
	token := strings.TrimSpace(string(data))
	raw, err := hex.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return nil, errors.New("Agent 凭据格式无效")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second}
	return &Client{base: strings.TrimSuffix(cfg.URL, "/"), token: token, http: &http.Client{Transport: transport, Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Agent 不接受重定向") }}}, nil
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

func (c *Client) get(ctx context.Context, path string, target any) error {
	return c.request(ctx, http.MethodGet, path, nil, target, false)
}

func (c *Client) request(ctx context.Context, method, path string, value, target any, long bool) error {
	var bodyReader io.Reader
	if value != nil {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(data)
	}
	r, err := http.NewRequestWithContext(ctx, method, c.base+path, bodyReader)
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	if value != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	client := c.http
	if long {
		copy := *client
		copy.Timeout = 10 * time.Minute
		transport := client.Transport.(*http.Transport).Clone()
		transport.ResponseHeaderTimeout = 10 * time.Minute
		copy.Transport = transport
		defer transport.CloseIdleConnections()
		client = &copy
	}
	response, err := client.Do(r)
	if err != nil {
		return fmt.Errorf("Agent HTTPS 连接失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 && response.StatusCode != 201 {
		var detail v1.Error
		_ = json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&detail)
		message := detail.Error.Message
		if len(message) > 1000 {
			message = "远端错误消息过长"
		}
		return &HTTPError{StatusCode: response.StatusCode, Code: detail.Error.Code, Message: message}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, manifest.MaxJSONBytes+1))
	if err != nil {
		return err
	}
	if len(body) > manifest.MaxJSONBytes {
		return errors.New("Agent 响应超过大小上限")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return fmt.Errorf("Agent 响应格式: %w", err)
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return errors.New("Agent 响应包含额外内容")
	}
	return nil
}

func (c *Client) Health(ctx context.Context) (v1.Health, error) {
	var value v1.Health
	err := c.get(ctx, "/v1/health", &value)
	return value, err
}
func (c *Client) Capabilities(ctx context.Context) (v1.Capabilities, error) {
	var value v1.Capabilities
	err := c.get(ctx, "/v1/capabilities", &value)
	return value, err
}
func (c *Client) Shares(ctx context.Context) (v1.ShareList, error) {
	var value v1.ShareList
	err := c.get(ctx, "/v1/shares", &value)
	return value, err
}

// Entries reads the first page using the default page size. Use EntriesPage for
// continuation or EntriesAll for a complete, bounded traversal.
func (c *Client) Entries(ctx context.Context, id, path string) (v1.EntryList, error) {
	return c.EntriesPage(ctx, id, path, 0, "")
}

// EntriesPage reads one stable, name-ordered directory page. A non-empty
// next_cursor must be passed back unchanged until has_more is false. If the
// directory changes between pages, the Agent returns cursor_stale and callers
// should restart from an empty cursor.
func (c *Client) EntriesPage(ctx context.Context, id, path string, limit int, cursor string) (v1.EntryList, error) {
	var value v1.EntryList
	if limit == 0 {
		limit = v1.DefaultEntryPageSize
	}
	if limit < 1 || limit > v1.MaxEntryPageSize || len(cursor) > 4096 {
		return value, errors.New("目录分页参数无效：limit 必须为 1 到 500，cursor 最长 4096 字节")
	}
	if path == "" {
		path = "."
	}
	query := url.Values{}
	query.Set("path", path)
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	err := c.get(ctx, "/v1/shares/"+url.PathEscape(id)+"/entries?"+query.Encode(), &value)
	if err == nil {
		err = validateEntryPage(value, id, path, limit, cursor)
	}
	return value, err
}

func validateEntryPage(page v1.EntryList, id, path string, limit int, cursor string) error {
	if page.ShareID != id || page.Path != path || page.Entries == nil || len(page.Entries) > limit || len(page.NextCursor) > 4096 || page.HasMore != (page.NextCursor != "") || (page.HasMore && (len(page.Entries) == 0 || page.NextCursor == cursor)) {
		return errors.New("Agent 目录分页响应不一致")
	}
	previous := ""
	for _, entry := range page.Entries {
		if !fs.ValidPath(entry.Name) || strings.Contains(entry.Name, "/") || entry.Name == "." || entry.SizeBytes < 0 || (entry.Directory && entry.SizeBytes != 0) || (previous != "" && entry.Name <= previous) {
			return errors.New("Agent 目录项目无效或未严格按名称排序")
		}
		previous = entry.Name
	}
	return nil
}

// EntriesAll aggregates pages only when the complete traversal succeeds. It
// does not return partial results on cursor_stale or silently retry a changing
// directory. Interactive callers should use EntriesPage to avoid buffering all
// metadata. The bound matches the Agent's directory scan limit.
func (c *Client) EntriesAll(ctx context.Context, id, path string, limit int) (v1.EntryList, error) {
	var result v1.EntryList
	cursor := ""
	for {
		page, err := c.EntriesPage(ctx, id, path, limit, cursor)
		if err != nil {
			return v1.EntryList{}, err
		}
		if len(result.Entries) > 0 && len(page.Entries) > 0 && page.Entries[0].Name <= result.Entries[len(result.Entries)-1].Name {
			return v1.EntryList{}, errors.New("Agent 目录分页出现重复或倒序项目")
		}
		if len(result.Entries)+len(page.Entries) > 100000 {
			return v1.EntryList{}, errors.New("目录超过 100000 项聚合上限，请逐页浏览")
		}
		if result.Entries == nil {
			result = page
		} else {
			result.Entries = append(result.Entries, page.Entries...)
			result.NextCursor, result.HasMore = page.NextCursor, page.HasMore
		}
		if !page.HasMore {
			return result, nil
		}
		cursor = page.NextCursor
	}
}

func (c *Client) Manifest(ctx context.Context, share, id string) (manifestv1.Published, error) {
	var value manifestv1.Published
	if !manifest.ValidID(id) {
		return value, errors.New("清单 ID 无效")
	}
	err := c.get(ctx, "/v1/shares/"+url.PathEscape(share)+"/manifests/"+id, &value)
	if err == nil {
		err = manifest.Validate(value.Manifest)
	}
	if err == nil && (value.Manifest.ManifestID != id || value.Manifest.ShareID != share || value.SFTPPath == "") {
		err = errors.New("Agent 清单与请求的版本或共享不一致")
	}
	return value, err
}
