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

前提：服务端已有可访问的 SSH/SFTP、专用账号和授权公钥。对端可为 Windows OpenSSH Server，也可以是其他兼容 SFTP 服务；本次本机集成测试使用 Go SSH/SFTP 真实 TCP 服务，尚未验收另一台电脑上的 Windows OpenSSH。

先在服务端通过可信渠道读取 SSH 主机公钥指纹，客户端用 `ssh-keyscan` 取得候选记录并用 `ssh-keygen -lf` 比较。只有指纹核对一致才把记录存为 known_hosts；`ssh-keyscan` 单独执行不能证明身份。
程序不会自动信任未知主机，也不会忽略主机密钥变化。

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
