# 配对资料 v1（P2.10）

本契约定义配对请求、回应 ZIP 与本机连接配置；与传输方式、在线配对服务及 Windows 管理员授权分开管理。

- 请求：`schema_version=1`、`kind=hopsend-pair-request`、32 位小写十六进制 `request_id`、规范的 `100.64.0.0/10` IPv4、完整 `ssh-ed25519` 公钥；可选显示名称 `name`。私钥留在发送端。
- 回应：不超过 256 KiB 的 ZIP，恰好包含 `ca.crt`、`client.token`、`device.json`，每项展开不超过 64 KiB；禁止额外项、重复项、子目录或链接。JSON 仅接受已声明字段，不接受重复键、无效 UTF-8 或额外 JSON 对象。
- `device.json` 对应 `ReplyDevice`；接收端地址必须同时为 `https://<tail-ip>:9443` 和 `<tail-ip>:22`，账号、共享和 Windows 绝对配置路径单独验证。SHA-256 的 CA 指纹计算对象是 `ca.crt` 原始文件字节；SSH 指纹按 OpenSSH SHA-256 规则计算。
- 调用方必须提供另行核对的 SSH 主机指纹与 CA 哈希。在线调用方还应绑定接收 IP 和请求 ID。自报指纹不能充当信任来源。
- 导入检查本机原请求及私钥匹配，固定回应的三个材料，不替换已有信任；`connection.json` 只保存文件路径。导入成功仅表示本地信任资料固定，真实 HTTPS/SSH/SFTP 连接验收由 `device-prepare` 完成。

Go DTO 见 `materials.go`；实际校验、密钥生成和原子导入位于 `internal/pairing/material`，CLI 入口是 `hopsend-cli pair request|inspect|import`。这些字段兼容原 P2.8 PowerShell 资料；新版对重复键、CA 内容和路径做更严格的检查。在线服务和接收端系统授权尚未由本阶段发布。

P2.11 将同一 ZIP 定义为 `.hopsend-pair` 文件；`preview` 只检查结构，必须返回 `trust_verified=false`。正式导入核对独立 SSH/CA 指纹并绑定本机原请求；`device.json` 的 JSON 排版差异可接受，所有 DTO 字段、CA 和 Token 的信任内容不能改变。桌面桥接和实际步骤见 [四步配对说明](../../../docs/22-desktop-pairing-wizard.md)。
