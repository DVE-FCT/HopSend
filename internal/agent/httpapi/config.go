package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

type Credential struct {
	ID        string   `json:"id"`
	TokenFile string   `json:"token_file"`
	ShareIDs  []string `json:"share_ids"`
}
type Share struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}
type Config struct {
	Listen          string       `json:"listen"`
	CertificateFile string       `json:"certificate_file"`
	KeyFile         string       `json:"key_file"`
	Credentials     []Credential `json:"credentials"`
	Shares          []Share      `json:"shares"`
}

var validID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func LoadConfig(filename string) (Config, error) {
	var cfg Config
	file, err := os.Open(filename)
	if err != nil {
		return cfg, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > 1<<20 {
		return cfg, errors.New("Agent 配置不可读取或超过 1 MiB")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("读取 Agent 配置: %w", err)
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return cfg, errors.New("Agent 配置必须只有一个 JSON 对象")
	}
	base, err := filepath.Abs(filepath.Dir(filename))
	if err != nil {
		return cfg, err
	}
	resolve := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	cfg.CertificateFile = resolve(cfg.CertificateFile)
	cfg.KeyFile = resolve(cfg.KeyFile)
	for i := range cfg.Credentials {
		cfg.Credentials[i].TokenFile = resolve(cfg.Credentials[i].TokenFile)
	}
	for i := range cfg.Shares {
		cfg.Shares[i].Path = resolve(cfg.Shares[i].Path)
	}
	return cfg, nil
}
