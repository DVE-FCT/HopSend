# 协议契约与存储规划

## 1. 契约分区

| 路径 | 后续文件 | 内容 |
|---|---|---|
| contracts/control/v1 | openapi.json、types.go | 已实现的 HTTPS 请求、响应、鉴权、错误码 |
| contracts/manifest/v1 | manifest.schema.json | 清单格式、块边界、内容 ID、路径约束 |
| contracts/events/v1 | events.schema.json / README.md | UI 事件名、载荷、节流和快照版本 |

当前 HTTPS GET 接口与 manifest v1 已落地 OpenAPI、JSON Schema 及共享 Go 类型；UI 事件和上传契约按 P2 推进。Go/TS DTO 若生成，注明生成来源。

## 2. 控制接口草案

```text
GET  /v1/health
GET  /v1/capabilities
GET  /v1/shares
GET  /v1/shares/{share_id}/manifests/{manifest_id}
GET  /v1/contents/{content_id}/availability
POST /v1/uploads
GET  /v1/uploads/{upload_id}
POST /v1/uploads/{upload_id}/finalize
```

前述路径混合当前接口与后续草案；以 contracts/control/v1/openapi.json 为实际接口依据。以 share_id/content_id/upload_id 访问受控对象，不接受任意绝对系统路径。

实际实现 GET /v1/health、/v1/capabilities、/v1/shares、/v1/shares/{share_id}/entries?path=相对目录，以及 P1.1 的 /v1/shares/{share_id}/manifests/{manifest_id}；目录接口支持 limit/cursor 分页，变化语义见 [ADR 0010](adr/0010-directory-pagination.md)；已实现的上传接口见 [接收端确认上传](14-confirmed-upload.md)。

## 3. Manifest 内容

当前 v1 包含 schema_version、manifest_id、content_id、share_id、name、size_bytes、chunk_size_bytes、chunks（index/offset/length/sha256）。name 是 Windows 安全单级文件名；content_id 同时承担完整文件 SHA-256 和不可变内容版本身份，不能把未实现的目录字段当作现有契约。

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

以上实体表是整体规划。实际 schema v3 包含 tasks、chunks、uploads 和 upload_chunks：下载保留完整 manifest、源引用、路径/状态及确认块；上传记录父目录及共享根/父目录身份、暂存和接收端确认。块范围和哈希来自原 manifest，不建立两份不一致元数据。v1/v2 升级先一致备份，旧会话不补造身份，详见 [ADR 0015](adr/0015-upload-parent-binding.md)。
其他节点、设置和未来调度实体仍按阶段实施。

## 5. UI 事件与状态

建议事件：tasks.snapshot、task.changed、nodes.snapshot、diagnostics.changed。聚合频率 250–500 ms，不逐网络包更新。
进度使用已验证已确认字节；传输量和重传量另计。全文件校验和发布有独立状态，不能提前显示已完成。

任务状态：准备中、传输中、已暂停、等待网络、需要处理、最终校验、发布中、已完成、已取消。
