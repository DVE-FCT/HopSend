package httpapi

import (
	"errors"
	"net/http"

	"github.com/DVE-FCT/HopSend/internal/publication"
)

func (s *Server) pathIndex(w http.ResponseWriter, r *http.Request, p *principal, id string) {
	if !p.shares[id] || s.roots[id] == nil || s.contentRoot == nil {
		fail(w, 404, "share_not_found", "共享不可访问或未配置内容库")
		return
	}
	query := r.URL.Query()
	if len(query) != 1 || len(query["path"]) != 1 || publication.ValidatePath(query.Get("path")) != nil {
		fail(w, 400, "invalid_path", "请指定共享内的规范相对文件或文件夹路径")
		return
	}
	if err := r.Context().Err(); err != nil {
		return
	}
	value, err := publication.Load(s.contentRoot, s.roots[id], id, query.Get("path"))
	if errors.Is(err, publication.ErrUnpublished) {
		fail(w, 404, "path_unpublished", publication.ErrUnpublished.Error())
		return
	}
	if err != nil {
		fail(w, 409, "index_stale", publication.ErrStale.Error())
		return
	}
	send(w, 200, value)
}
