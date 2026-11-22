# 协议契约与存储规划

## 1. 契约分区

| 路径 | 后续文件 | 内容 |
|---|---|---|
| contracts/control/v1 | openapi.json、types.go | 已实现的 HTTPS 请求、响应、鉴权、错误码 |
| contracts/manifest/v1 | manifest.schema.json | 清单格式、块边界、内容 ID、路径约束 |
| contracts/events/v1 | events.schema.json / README.md | UI 事件名、载荷、节流和快照版本 |

P0.3 的四个 GET 接口已落地 OpenAPI 3.1 与共享 Go DTO，实际响应已按契约检查。清单、UI 事件和上传接口仍在规划中。Go/TS DTO 若生成，注明生成来源。

## 2. 控制接口草案

```text
GET  /v1/health
GET  /v1/capabilities
GET  /v1/shares
GET  /v1/manifests/{manifest_id}
GET  /v1/contents/{content_id}/availability
POST /v1/uploads
GET  /v1/uploads/{upload_id}
POST /v1/uploads/{upload_id}/finalize
```

前述路径是规划；不代表已经有服务监听。以 share_id/content_id/upload_id 访问受控对象，不接受任意绝对系统路径。

P0.3 当前实际实现 GET /v1/health、/v1/capabilities、/v1/shares，以及 /v1/shares/{share_id}/entries?path=相对目录；其余清单与上传路径尚未实现。

## 3. Manifest 内容

schema_version、manifest_id、content_id、relative_path、size_bytes、chunk_size_bytes、chunks（index/offset/length/sha256）、file_sha256、source_version。

文件内容 ID 根据字节哈希生成；清单 ID 根据规范化清单生成，排除 ID 自身和签名。不同块规则的清单即使内容相同，也不能直接混用分块状态。
版本发布后保持只读；内容改变生成新版本。预期哈希从已认证 Agent 取得，未来公开分享再加入签名与信任管理。

## 4. SQLite 实体

| 实体 | 要保存什么 |
|---|---|
| nodes | 节点地址、能力、信任与凭据引用 |
| manifests/files | 清单 ID、内容 ID、大小和分块参数 |
| tasks | 方向、目标、状态、冲突策略、最近错误 |
| task_files | 暂存与最终路径、确认字节和文件状态 |
| chunks | 块号、偏移、长度、哈希和确认状态 |
| attempts/leases | 来源节点、尝试世代、超时和结果 |
| upload_sessions | Agent 端上传目标、块确认及 finalize 状态 |
| settings | 并发、限速、默认目录等非机密设置 |

Agent 与桌面进程使用各自的数据库文件。共用迁移基础设施时，按角色明确 schema 范围，防止服务和 UI 进程同时争用一个业务库。
开启适当 WAL/事务策略，使用受控写入路径聚合提交。WAL 不替代文件数据 Sync。

## 5. UI 事件与状态

建议事件：tasks.snapshot、task.changed、nodes.snapshot、diagnostics.changed。聚合频率 250–500 ms，不逐网络包更新。
进度使用已验证已确认字节；传输量和重传量另计。全文件校验和发布有独立状态，不能提前显示已完成。

任务状态：准备中、传输中、已暂停、等待网络、需要处理、最终校验、发布中、已完成、已取消。
