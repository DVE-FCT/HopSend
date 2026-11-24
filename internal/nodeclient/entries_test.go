package nodeclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
)

func TestEntriesAllRejectsBrokenContinuationWithoutPartialResults(t *testing.T) {
	for _, scenario := range []string{"duplicate", "repeated_cursor", "empty_continuation", "wrong_scope", "stale"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls > 2 {
					t.Error("client continued after invalid pagination")
					http.Error(w, "unexpected request", http.StatusInternalServerError)
					return
				}
				page := v1.EntryList{ShareID: "files", Path: ".", Entries: []v1.Entry{{Name: "a.txt"}}, HasMore: true, NextCursor: "next"}
				if calls == 2 {
					switch scenario {
					case "duplicate":
						page.HasMore, page.NextCursor = false, ""
					case "repeated_cursor":
						page.Entries[0].Name = "b.txt"
					case "empty_continuation":
						page.Entries = []v1.Entry{}
						page.NextCursor = "other"
					case "wrong_scope":
						page.ShareID = "other"
					case "stale":
						w.WriteHeader(http.StatusConflict)
						_ = json.NewEncoder(w).Encode(v1.Error{Error: v1.ErrorDetail{Code: "cursor_stale", Message: "目录已变化"}})
						return
					}
				}
				_ = json.NewEncoder(w).Encode(page)
			}))
			defer server.Close()
			// This test exercises response validation. TLS/authentication are tested
			// using the production constructor in tests/integration.
			client := &Client{base: server.URL, http: server.Client()}
			result, err := client.EntriesAll(context.Background(), "files", ".", 1)
			if err == nil || result.Entries != nil || calls != 2 {
				t.Fatalf("partial/broken result accepted: entries=%v calls=%d err=%v", result.Entries, calls, err)
			}
		})
	}
}
