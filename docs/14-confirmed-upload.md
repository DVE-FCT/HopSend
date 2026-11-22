# P2.2 接收端确认上传

## 接收端配置

现有只读共享不会自动开放上传。新增独立接收目录和共享，并给指定 API 身份增加上传授权：

```json
{
  "shares": [
    {"id":"files","path":"D:/HopSendShare"},
    {"id":"inbox","path":"D:/HopSendInbox","writable":true}
  ],
  "credentials": [
    {"id":"paired-device","token_file":"client.token","share_ids":["files","inbox"],"upload_share_ids":["inbox"]}
  ],
  "state_directory":"state"
}
```

这是追加字段的局部示意，须与现有 listen、certificate_file、key_file 等配置合并，不能把片段直接当完整配置。
接收目录必须提前创建，Agent 与 SSH 专用账号都需具备所需 NTFS 权限。API 写入授权与 SSH 文件权限分别检查；无需管理员即可在已授权普通用户目录启动前台 Agent，防火墙仍遵守原网络限制。

## CLI 上传

```powershell
./hopsend-cli.exe upload --url 'https://接收设备地址:9443' `
  --ca-cert 'CA公钥路径' --token-file '本设备凭据路径' `
  --address '接收设备地址:22' --user 'SSH专用账号' `
  --key '私钥路径' --known-hosts '已核对主机记录' `
  --share inbox --source 'D:\Files\report.bin' --target 'report.bin' `
  --database 'D:\HopSendData\tasks.sqlite' --chunk-mib 32
```

程序先生成本机不可变快照，保留到任务数据区的 upload-content；需要源文件之外的额外空间。源文件后续改变不修改已有版本。
重跑相同上传或运行 resume/control 的原任务 ID，会使用原版本及接收端重新核验的确认记录。发送字节结束后必须等待接收端 finalize，不能提前显示已完成。

## 桌面操作

在传输任务选择“上传到设备”，选择接收设备、接收共享 ID（通常为 inbox）、本机源文件和接收文件名。
生成版本阶段会显示正在准备，真实任务沿用相同 Manager，支持运行中的暂停、继续、取消与网络恢复。目标链接表示远端共享内文件，不是浏览器下载链接。
当前只支持单个文件、安全单级文件名；目录任务在 P2.3 实现。

## 协议与恢复规则

- POST /v1/uploads：受认证身份的幂等会话，检查共享授权、目标冲突和可用容量。
- GET /v1/uploads/{id}：接收端重新读取已确认块，坏块撤销确认。
- POST /v1/uploads/{id}/chunks/{index}/confirm：接收端核验、Sync、SQLite 确认。
- POST /v1/uploads/{id}/finalize：接收端完整校验与禁止覆盖发布，返回 completed 和匹配 SHA-256。

SFTP 只写 Agent 分配的既有暂存文件，不截断正式目标。完成后会话仍可查询并核验；其他身份不能通过猜测会话 ID 访问。
容量检查不能预留空间；磁盘不足、权限、坏块、不同内容版本和目标同名都必须报告失败，未验证内容不发布。
Agent 数据库从 v1 事务迁移至 v2，增加会话和确认块表；旧下载任务保留。不要用旧程序打开已迁移的新数据库。

上传状态与发布对账见 [ADR 0004](adr/0004-receiver-confirmed-upload.md)，API 类型与字段见 contracts/control/v1/openapi.json。
