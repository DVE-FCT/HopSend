# 已发布路径与版本下载

P2.16a 提供路径索引和源端显式发布；桌面点选下载由 P2.16b 接入。以下 CLI 在源设备本机运行，普通浏览不会复制或哈希源文件。

## 源端发布

使用配置中已存在的共享 ID 和共享内相对路径：

```powershell
hopsend-cli.exe publish-path --agent-config C:\HopSend\agent.local.json --share files --path "资料/报告.pdf"
```

同时支持文件夹和空文件。文件夹生成独立目录包，快照所占字节与目录列表大小含义不同。内容库不能位于正在发布的文件夹内。一个内容库同时只允许一个发布操作；遇到忙提示稍后重试。Ctrl+C 取消不会切换旧索引。发布耗时和空间随文件大小增长，所有者应确保磁盘容量充足。

返回相对路径、`manifest_id`、`content_id`、`size_bytes`、`payload_kind`、`published_at`。源文件发生改变时需要再次显式发布；正在进行的下载仍固定旧版本，不自动切换。

## 客户端查询

```powershell
hopsend-cli.exe path-index --url https://your-node:9443 --ca-cert C:\HopSend\ca.crt --token-file C:\HopSend\client.token --share files --path "资料/报告.pdf"
```

查询走受认证 HTTPS，文件内容继续由现有下载任务通过 SFTP 读取。`path_unpublished` 表示尚未发布，`index_stale` 表示索引/清单/对象不可用；请由源端重新发布，不按同名或同大小推断版本。只有上传许可的凭据不能查看索引。旧 Agent 缺少 `published_path_index` 能力时需要升级，或继续使用现有清单 ID 入口。

索引是“最后一次显式发布的快照”，不证明实时源文件仍与快照相同。客户端必须显示快照名称、大小、类型及发布时间并由用户选定，不能把目录实时元数据冒充已发布内容。版本完整性依旧由 manifest、块 SHA-256 和全文件 SHA-256 校验。

决定与边界见 [ADR 0014](adr/0014-published-path-index.md)，实际检查见 [P2.16a 验收](validation/P2.16a-published-path-index.md)。
