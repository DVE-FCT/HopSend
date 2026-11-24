# P0 基础连通：当前能力与使用方法

## P0.1 工具链与 CLI

固定版本保存在 configs/toolchain.json，CLI 从同一文件嵌入预期值。
Go 1.27.4 下载包 SHA-256 与 go.dev 官方发布记录比对；Wails CLI 采用 v2.17.1。
Node 24.14.1 和 pnpm 11.25.0 已在本机实际执行检查。

```powershell
./scripts/dev/bootstrap-tools.ps1
# 如 Go 无法直连模块代理，而 Windows 已配置系统代理：
./scripts/dev/bootstrap-tools.ps1 -UseSystemProxy
. ./scripts/dev/env.ps1
go run ./apps/cli version
go run ./apps/cli doctor
go run ./apps/cli doctor --strict
./scripts/check/check-go.ps1 -StrictDoctor
```

引导脚本使用项目内 work/.tools（Go 自动遍历会跳过点号目录），验证下载包后才解压。不覆盖系统 Go，也不修改永久 PATH。
env.ps1 仅作用于当前 PowerShell 会话，并设置 GOTOOLCHAIN=local，防止隐式下载其他版本。

输出为 JSON。doctor 的状态为 ok、unavailable、version_mismatch；非严格模式保留诊断结果并成功退出，严格模式在不满足契约时退出 1。
未知命令或参数退出 2；取消或运行失败退出 1。version 可独立于工具链存在而运行。

doctor 仅检查开发工具执行与版本。Wails CLI 可执行不代表本机桌面运行时和打包依赖已验收。
当前 Go 主模块只有 CLI 与诊断，不引入 Wails runtime，也没有 Vue package.json；桌面开发在 P2.1。

## 构建验证

check-go.ps1 执行 gofmt 检查、go vet、无缓存测试、CLI 构建及 version 冒烟；-StrictDoctor 追加固定工具版本检查。
GitHub Actions 的 windows 作业对测试分支与 main 执行同一脚本，尚未有桌面项目时不会假装运行前端构建。

P0.1 的干净源码导出构建使用暂存树生成独立归档，在没有 node_modules、Wails 自动绑定或现有编译产物的目录测试。
开发工具是构建前提，并不把“干净源码”写成已验证全新 Windows 系统安装。

## P0.2 SSH/SFTP 范围诊断

前提：服务端已有可访问的 SSH/SFTP、专用账号和授权公钥。对端可为 Windows OpenSSH Server，也可以是其他兼容 SFTP 服务；本机集成测试使用 Go SSH/SFTP 真实 TCP 服务，已另行完成第二台 Windows OpenSSH 的实测，范围见 docs/validation/P0.2-device.md。

先在服务端通过可信渠道读取 SSH 主机公钥指纹，客户端用 `ssh-keyscan` 取得候选记录并用 `ssh-keygen -lf` 比较。只有指纹核对一致才把记录存为 known_hosts；`ssh-keyscan` 单独执行不能证明身份。
程序不会自动信任未知主机，也不会忽略主机密钥变化。
服务器同时提供多个主机密钥算法时，程序依据目标主机在 known_hosts 中已核对的记录协商，只采用当前 SSH 库列为安全的算法，并继续执行完整公钥比对。不会因协商失败自动添加其他主机密钥；普通和散列主机名记录均支持。

Windows OpenSSH 的绝对盘符路径通常表现为 SFTP 路径 `/C:/共享目录/文件名`；Windows 资源管理器的 `C:\共享目录\文件名` 需要按服务端 SFTP 的路径规则使用。

```powershell
. ./scripts/dev/env.ps1
# 以下地址和路径为示例，需要改成已授权的真实设备与文件
go run ./apps/cli sftp-read `
  --address "100.100.10.20:22" --user "transfer" `
  --key "C:\keys\hopsend_ed25519" --known-hosts "C:\keys\known_hosts" `
  --remote "/shared/sample.bin" --offset 4096 --length 1048576 --timeout 30s
```

输出精确读取的范围 SHA-256、字节数与耗时。提供 `--sha256 <可信范围哈希>` 时才会输出 expected_hash_verified=true；否则只报告测量值，不声称内容来源已验证。

命令只读取、哈希并丢弃范围字节，不创建下载文件、任务或续传记录。请求长度超出源文件、非法范围、身份/主机校验失败均返回非零；Ctrl+C 和超时会关闭该 worker 的 SSH 连接以解除阻塞。
当前只支持文件型未加密私钥，不提供密码、私钥口令或 SSH Agent 交互；应使用专用且受文件权限保护的测试密钥。

```powershell
go test -v -count=1 -timeout=90s ./tests/integration
```

测试通过真实回环 TCP 建立加密 SSH 会话和 SFTP 子系统，读取临时文件。覆盖范围内容、CLI 可信哈希、未知/变化主机、错误用户私钥、截断源、整数溢出、读取中取消和不发送握手的超时服务。
回环测试不证明局域网性能、Tailscale 穿透、DERP 路径或第二台设备已连通。两设备演练步骤见 [P0 验收运行手册](09-p0-device-runbook.md)。
独立现场记录已补充两设备正确性验证；当前观测为同一局域网直连，不代表不同公网网络、DERP 吞吐或大文件性能已经通过。

## P0.3 HTTPS Agent

当前实现四个受认证 GET 接口：health、capabilities、shares、shares/{share_id}/entries。目录接口支持按名称稳定排序的 limit/cursor 分页，默认每页 100 项、最大每页 500 项。
正式契约为 contracts/control/v1/openapi.json（OpenAPI 3.1）。API 明确返回 upload_supported=false；没有上传、manifest 或内容中转能力。

```powershell
. ./scripts/dev/env.ps1
# 可选的 --share-dir 必须是你明确授权的已有目录；不填写则不共享文件
go run ./apps/cli agent-init --dir work/local-agent --share-dir "D:\Share"
go run ./apps/agent --config work/local-agent/agent.local.json
```

另一个终端：

```powershell
. ./scripts/dev/env.ps1
go run ./apps/cli agent-status --url https://localhost:9443 `
  --ca-cert work/local-agent/ca.crt --token-file work/local-agent/client.token `
  --share files --path .
```

使用默认监听 127.0.0.1:9443；若占用，在配置中改端口。初始化目录必须为空，生成失败不会覆盖已有文件。
初始化会创建 CA 公钥、30 天服务器证书、服务器私钥、随机 256 位 API Token 和配置。CA 签名私钥不落盘；证书到期后需重新生成并重新配对信任，首版没有自动续证。
Windows 上 0600 并不等于完整 NTFS ACL 配置，生成目录仅供当前用户开发；部署到其他设备前需确认凭据和私钥实际 ACL。

共享配置使用 id/path，身份配置使用 id/token_file/share_ids。每台设备应分配不同随机凭据，授权共享列表分别设置；权限不自动同步到 SSH 账号。
服务端拒绝重复凭据、重复 ID 或不存在的共享引用，读取配置时拒绝未知字段。

目录接口只返回相对名称、类型和大小，不返回绝对系统路径。拒绝 ..、绝对路径、反斜线、冒号、NUL 以及 symlink/junction 遍历；使用 os.Root 进一步约束路径解析。请求可带 `limit=1..500`，响应包含 `next_cursor` 和 `has_more`。游标绑定共享、相对目录和目录快照指纹；目录内容或可见元数据在分页期间变化时，后续请求返回 HTTP 409 `cursor_stale`，客户端应丢弃游标并从第一页重新读取。这样可以避免新增、删除或重命名造成静默漏项和重复。

CLI 单页查询示例：

```powershell
go run ./apps/cli agent-status --url https://localhost:9443 `
  --ca-cert work/local-agent/ca.crt --token-file work/local-agent/client.token `
  --share files --path . --limit 200
```

需要一次读取全部页面时使用 `--all`。它会在目录变化时失败并报告 `cursor_stale`，由调用方重新执行：

```powershell
go run ./apps/cli agent-status --url https://localhost:9443 `
  --ca-cert work/local-agent/ca.crt --token-file work/local-agent/client.token `
  --share files --path . --limit 200 --all
```

客户端验证 TLS 1.3 证书链和实际主机名，只信任显式提供的 CA，不跳过检查，不接受重定向；所有控制接口都需要 Bearer 凭据。
凭据存放文件而非命令行参数，日志与状态不输出凭据内容。CA 公钥仍需经可信渠道核对初始化时输出的 SHA-256。

## 编译产物

运行 check-go.ps1 后，可直接执行 dist/cli/hopsend-cli.exe 与 dist/agent/hopsend-agent.exe；这两个文件尚未制作安装器。
Agent 当前为前台进程，Ctrl+C 停止。没有安装后台服务、修改防火墙或启用 Windows SSH 服务。

## 目录分页边界（P2.9 更新）

目录名称按 UTF-8 字节序排序；游标由当前 Agent 实例签名，不能跨共享、目录或实例复用。Agent 重启后旧游标返回 400 `invalid_cursor`，需要从第一页重读。每一页仍需提供原有 Token 和共享授权。客户端和 Agent 应同批升级：旧客户端会拒绝新增响应字段；新版调用方必须处理 `has_more`，或使用 `EntriesAll` / CLI `--all`。

每页扫描目录元数据以检测变化，扫描最多 100000 项，超限返回 413；大目录和小页长会增加扫描开销。本功能未提供事务快照；并发修改或保持相同大小/时间的同名替换可能无法完整识别。文件内容版本继续由可信 manifest 与 SHA-256 校验保证。目录包传输自身的 10000 项限制不受此修改影响。本阶段不新增桌面目录浏览界面。
