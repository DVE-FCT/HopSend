# 桌面四步配对与单文件配对包

本页是当前桌面客户端的新流程。早期 `09-p0-device-runbook.md` 中逐项安装、编辑 JSON、复制资料的步骤保留为底层诊断运行手册；正常客户端配对不需要填写八个字段。

## 客户端：四步配对

1. 打开“设备与信任 → 配对新设备”。向导读取本机正在运行的 Tailscale 状态，显示唯一的 IPv4 和设备名称。读取失败可手动填写本机地址和名称；生成时必须在本机已启用的 Tailscale 网卡中找到该 IPv4，不能填其他设备地址。状态可读取时，以状态为准。确认地址后点击“生成配对请求”，把一个公开 JSON 交给源设备。也可重新导出已配对设备的原请求，复用原密钥。
2. 源设备返回 `reply.zip` 或 `.hopsend-pair` 后，点击“选择回应 / 配对包”；`.hopsend-pair` 也可拖入桌面窗口。显示设备名称、Agent、SSH 账号、共享与两个指纹，无需手动填连接字段。
3. 在源电脑或其他可信渠道核对完整 SSH 主机指纹和 CA SHA-256，填入两项指纹并勾选“已通过可信渠道核对”。包内自报的名称和指纹不是独立信任来源。更换回应会清除已有确认。
4. 点击“完成配对并验收”。导入绑定本机原请求/私钥并保存设备，然后自动验收连接：启用 manage_agent 的设备执行准备和实际 HTTPS/SFTP 字节检查；非托管设备只做 HTTPS 与 SSH/SFTP 认证，不部署、不宣称文件字节已验证。结果回到设备列表。失败明确显示“配对资料已保存，但连接未通过”，无需重复配对，可点“诊断此设备”跳转并预选目标。

应用打开期间每 30 秒通过 ObserveDevice 只读检查 HTTPS health，最多同时检查两台；手动刷新复用同一队列。健康点只表示 HTTPS 可达，不代表文件权限或传输已验收。关闭界面停止定时器和后续排队；已发出的只读请求使用后端超时。诊断页不会因为预选目标自动发起文件读取。

请求和信任资料在当前账号的应用数据目录，返回的公开请求路径可复制或在资源管理器中找到。公开请求不含私钥。回应和配对包含 API Token，只交给对应请求电脑，不公开分享。

## 已配对设备的信任轮换

远端换证书或 SSH 主机密钥后，在原设备卡片点“轮换信任”。导出该设备原公开请求，让源电脑针对它生成新回应；选择新包，重新通过可信渠道核对完整 SSH/CA 指纹，点“确认轮换并验收”。仍使用原设备 ID、本机私钥、显示名称和托管选择。

轮换会保留旧凭据路径和任务历史，不删除再建设备。旧任务仍绑定旧授权，远端撤销旧授权后可能无法继续；此操作不会自动迁移任务凭据。新资料写入失败时，当前设备引用保持不变；网络验收失败时保留已选定的新资料，不自动回退旧身份。普通配对导入不能覆盖轮换后的引用。见 [ADR 0013](adr/0013-versioned-trust-rotation.md)和 [P2.13 验收](validation/P2.13-trust-rotation.md)。

## 配对包格式

`.hopsend-pair` 是有大小限制的 ZIP，使用既有三个材料：`device.json`、`ca.crt` 和 `client.token`。`device.json` 保存 SSH 主机公钥；客户端据已核对的公钥生成 `known_hosts`，无需另行选文件。SSH 私钥始终由客户端生成并保留，不进入配对包。

导出使用新的文件名，不覆盖已有文件；只重建三个允许的 ZIP 项，不复制额外条目或尾部载荷。导入前预览只检查结构与自洽性，返回 `materials_verified=true` 和 `trust_verified=false`。正式导入仍需传入可信指纹，不能用预览绕过核对。

## 源设备准备入口的交付边界

P2.8 本地 preview.15 的“设备准备 → 本机提供文件”提供图形入口：选择客户端公开请求、共享目录和上传权限，确认管理员提示后自动准备环境、固定公钥、启动 Agent 并导出一个配对包，显示供核对的完整指纹。已有同一请求可重用原授权；其他接收身份占用端口时拒绝覆盖原配置。

对应脚本为预览包内 `Prepare-Source.ps1`，工程入口为 `scripts/setup/prepare-source.ps1`；它们复用 P2.8 生命周期和标准账号授权，不属于本次独立客户端 PR。源设备首次 Tailscale 登录、管理员授权和标准账号登录仍由设备所有者完成。源设备的物理现场结果、干净 Windows 前提和 P2.8 阶段状态单独记录，不能以客户端测试替代。

## 开发接口和 CLI

Wails 在 `App` 上暴露：

```go
PairLocalIdentity() (PairIdentity, error)
PairCreateRequest(sourceIP, name string) (PairRequestResult, error)
PairInspectReply(replyPath string) (PairInspectResult, error)
PairImportReply(replyPath, expectedSSH, expectedCA string) (string, error)
RotateDeviceTrust(id, replyPath, expectedSSH, expectedCA string) (Device, error)
```

方法实现按职责放在 `apps/desktop/pairing_material_windows.go`，仍由同一个 `App` 绑定到桌面。`bridge.ts` 声明相同参数和返回值。`PairImportReply` 只接受近期检视过且哈希未改变的回应；自动保存的设备 ID 为原请求 ID，相同回应不重复新增。

CLI 新增 `pair inspect-request --request <公开JSON>`、`pair preview --reply <包>` 和 `pair package --reply <既有回应ZIP> --output <新.hopsend-pair> --expected-ssh <可信指纹> --expected-ca <可信CA哈希>`。`preview` 明确返回未验证身份，不写信任目录；已有 request/inspect/import 继续兼容。

文件拖放由顶层窗口统一分流：`.hopsend-pair` 进入配对；混合配对包和普通文件被拒绝；普通 ZIP 在上传页仍是普通文件，不自动当作配对。普通文件拖入不会自动开始上传。

客户端初始交付见 [P2.11](validation/P2.11-desktop-pairing.md)，连接闭环见 [P2.12](validation/P2.12-pairing-lifecycle.md)，设计见 [ADR 0012](adr/0012-desktop-pairing-packages.md)。
