# Go 配对资料核心与 CLI

P2.10 提供可运行的 `pair request`、`pair inspect` 和 `pair import`。发送端生成密钥、校验回应和固定信任由 Go 完成，正常调用不依赖 PowerShell 或系统 `ssh-keygen`。接收端仍需通过现有准备流程确认共享与授权，并生成回应；本阶段未发布完整在线配对 UI 和安装服务。

## 生成请求

先核对本机 Tailscale IPv4，以下地址为示例。`--source-ip` 填写已核对的地址，不会自动登录 Tailscale 或证明网络连接正常。`--trust-dir` 使用本账号控制的专用本地目录；示例放在被 Git 忽略的 `work/` 内。

```powershell
New-Item -ItemType Directory -Path work/pair-output -Force | Out-Null
go run ./apps/cli pair request --trust-dir work/pair-trust `
  --out-dir work/pair-output --source-ip 100.100.10.20 --name "我的电脑"
```

命令返回公开请求内容和 `request_file`。把该请求交给接收端准备流程；`trust/<request_id>/id_ed25519` 始终保存在本机，不参与交换。请求可选不导出，此时 `request_file` 指向信任目录内的原请求。

Windows 私钥目录在创建时就限制为当前账号和 SYSTEM；Linux/macOS 只有资料内核的 Unix 权限适配，不能据此宣称完整应用跨平台已验收。

## 检查回应并固定信任

从可信渠道核对 SSH 主机指纹和 CA 文件 SHA-256。不要直接照抄未经核对的回应自报值。示例中的文件和占位值需换成实际资料。

```powershell
go run ./apps/cli pair inspect --reply work/pair-output/reply.zip `
  --expected-ssh "<已核对的 SHA256:...>" --expected-ca "<已核对的64位CA哈希>" `
  --request-id "<32位请求ID>" --receiver-ip "<接收端Tailscale IPv4>"

go run ./apps/cli pair import --trust-dir work/pair-trust `
  --reply work/pair-output/reply.zip `
  --expected-ssh "<已核对的 SHA256:...>" --expected-ca "<已核对的64位CA哈希>" `
  --request-id "<32位请求ID>" --receiver-ip "<接收端Tailscale IPv4>"
```

在线调用方必须绑定本次接收 IP 和请求 ID。离线 CLI 可省略这两个参数，但仍会严格匹配本机原请求和私钥。回应中不能包含私钥，ZIP 不会按外部提供的路径解压；只接收三个固定文件名。

导入成功返回 `connection_file` 和 `verification: "not_run"`。文件中只含 CA、Token、私钥及 known_hosts 的路径引用。相同回应重试不会生成另一套信任；材料或派生连接配置不同会返回 `trust_conflict`，不会静默替换旧信任。

本地导入不发起传输。要完成真实连接验收，再执行：

```powershell
go run ./apps/cli device-prepare --device-config "<返回的connection_file>" `
  --agent-binary dist/agent/hopsend-agent.exe
```

该步骤会连接已授权设备，检查 HTTPS、SSH 与 SFTP；需要先构建配套 Agent，或使用包含该 EXE 的分发包。失败后可以使用同一回应重试，不必替换已经核对的信任。

## 错误与范围

业务失败退出 1，并在 stderr 返回包含 `stage`、`code`、`message` 的 JSON；参数缺失或格式错误退出 2。输出不包含 Token 或私钥原文。常见代码为 `binding_mismatch`、`ssh_fingerprint_mismatch`、`ca_fingerprint_mismatch`、`private_key_mismatch`、`trust_conflict`、`import_busy` 和 `cancelled`。

资料协议见 [配对 v1](../contracts/pairing/v1/README.md)，设计依据见 [ADR 0011](adr/0011-native-pairing-materials.md)，实际测试与未覆盖范围见 [P2.10 验收](validation/P2.10-native-pairing-materials.md)。
