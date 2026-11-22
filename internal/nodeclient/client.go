package nodeclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
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
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.http.Do(r)
	if err != nil {
		return fmt.Errorf("Agent HTTPS 连接失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("Agent 返回 HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
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
func (c *Client) Entries(ctx context.Context, id, path string) (v1.EntryList, error) {
	var value v1.EntryList
	err := c.get(ctx, "/v1/shares/"+url.PathEscape(id)+"/entries?path="+url.QueryEscape(path), &value)
	return value, err
}
