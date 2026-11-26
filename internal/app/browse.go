package app

import (
	"context"
	"errors"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
)

// Browsing never receives SSH credentials or the managed-Agent configuration.
// These calls cannot deploy software, publish snapshots or transfer file bytes.
type BrowseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type BrowseSharesResult struct {
	Shares     []v1.Share   `json:"shares"`
	ObservedAt string       `json:"observed_at"`
	Error      *BrowseError `json:"error,omitempty"`
}

type BrowsePageResult struct {
	Page       *v1.EntryList `json:"page,omitempty"`
	ObservedAt string        `json:"observed_at"`
	Error      *BrowseError  `json:"error,omitempty"`
}

var browseShareID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func browseFailure(err error) *BrowseError {
	var remote *nodeclient.HTTPError
	if errors.As(err, &remote) {
		return &BrowseError{Code: remote.Code, Message: remote.Error()}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &BrowseError{Code: "browse_timeout", Message: "目录查询已取消或超时，请确认节点在线后重试"}
	}
	return &BrowseError{Code: "browse_failed", Message: err.Error()}
}

func browseClient(ctx context.Context, cfg nodeclient.Config) (*nodeclient.Client, *BrowseError) {
	client, err := nodeclient.New(cfg)
	if err != nil {
		return nil, browseFailure(err)
	}
	caps, err := client.Capabilities(ctx)
	if err != nil {
		client.Close()
		return nil, browseFailure(err)
	}
	if caps.APIVersion != "v1" || !slices.Contains(caps.Operations, "list_entries_paginated") || !slices.Contains(caps.Operations, "shares") {
		client.Close()
		return nil, &BrowseError{Code: "browse_unsupported", Message: "此 Agent 不支持目录分页，请先升级并验收节点；不会退回可能截断的旧接口"}
	}
	return client, nil
}

func browseShares(ctx context.Context, client *nodeclient.Client) ([]v1.Share, *BrowseError) {
	value, err := client.Shares(ctx)
	if err != nil {
		return nil, browseFailure(err)
	}
	if value.Shares == nil || len(value.Shares) > 10000 {
		return nil, &BrowseError{Code: "invalid_shares", Message: "Agent 共享列表无效或超过上限"}
	}
	seen := make(map[string]bool)
	for _, share := range value.Shares {
		if !browseShareID.MatchString(share.ID) || seen[share.ID] || (share.Access != "read_only" && share.Access != "read_write" && share.Access != "write_only") {
			return nil, &BrowseError{Code: "invalid_shares", Message: "Agent 返回了重复、非法或未知权限的共享"}
		}
		seen[share.ID] = true
	}
	return value.Shares, nil
}

func BrowseShares(parent context.Context, cfg nodeclient.Config) BrowseSharesResult {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	result := BrowseSharesResult{Shares: []v1.Share{}, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	client, failure := browseClient(ctx, cfg)
	if failure != nil {
		result.Error = failure
		return result
	}
	defer client.Close()
	shares, failure := browseShares(ctx, client)
	if failure != nil {
		result.Error = failure
	} else {
		result.Shares = shares
	}
	return result
}

func BrowseEntries(parent context.Context, cfg nodeclient.Config, share, path, cursor string, limit int) BrowsePageResult {
	result := BrowsePageResult{ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if path == "" {
		path = "."
	}
	if !browseShareID.MatchString(share) || !utf8.ValidString(path) || len(path) > 4096 || !fs.ValidPath(path) || strings.ContainsAny(path, "\\:\x00") || len(cursor) > 4096 || limit < 1 || limit > v1.MaxEntryPageSize {
		result.Error = &BrowseError{Code: "invalid_browse_request", Message: "目录查询需要共享内相对路径、1～500 项页长和有效游标"}
		return result
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	client, failure := browseClient(ctx, cfg)
	if failure != nil {
		result.Error = failure
		return result
	}
	defer client.Close()
	shares, failure := browseShares(ctx, client)
	if failure != nil {
		result.Error = failure
		return result
	}
	allowed := slices.ContainsFunc(shares, func(s v1.Share) bool {
		return s.ID == share && (s.Access == "read_only" || s.Access == "read_write")
	})
	if !allowed {
		result.Error = &BrowseError{Code: "browse_denied", Message: "此共享不在读取授权内；上传许可不包含目录读取权"}
		return result
	}
	page, err := client.EntriesPage(ctx, share, path, limit, cursor)
	if err != nil {
		result.Error = browseFailure(err)
	} else {
		for _, entry := range page.Entries {
			if !utf8.ValidString(entry.Name) || strings.ContainsAny(entry.Name, "\\:\x00") {
				result.Error = &BrowseError{Code: "invalid_entries", Message: "目录项目含不受支持的路径名称，未返回部分列表"}
				return result
			}
		}
		result.Page = &page
	}
	return result
}
