# 开发环境与构建规划

## 当前检测结果

2026-11-22 创建骨架时，在 PATH 中找到 Node.js、npm/pnpm、Git；没有找到 Go 与 Wails。
尚未核验工具版本兼容性，没有安装依赖，没有生成或构建业务程序。

## 需要准备

- 受支持的稳定 Go 版本：项目启动时确认并固定 toolchain。
- 所选 Wails 稳定主版本及其 Windows 构建依赖；按该版本官方要求准备。
- 与 Vite/Vue 兼容的 Node LTS 和固定 pnpm 版本。
- WebView2 运行时；开发工具是否需要额外平台依赖按实际 Wails 版本确认。
- Git；SSH/SFTP 与跨网络验证时才需要对端 OpenSSH 和 Tailscale。

开发机不需要仅为浏览代码就启用 OpenSSH Server。服务端功能在作为源/接收服务节点时配置。

## 根 Go 模块

环境就绪后，在工程根目录执行：

```powershell
Set-Location 'C:\Users\Lenovo\OneDrive\Desktop\HopSend'
go mod init github.com/DVE-FCT/HopSend
```

该命令用于首次初始化；go.mod 已存在时不重复执行。Go 版本由选定工具链生成并在评审时固定。

不要在 apps/desktop、apps/agent、apps/cli 内再次 go mod init，也不要直接使用会生成嵌套模块的默认模板覆盖现有结构。
Wails 模板可先在临时目录查看，然后适配到 apps/desktop，保留一份根模块。

## 后续入口实现后的命令

以下命令在对应 main.go 与依赖完成后才能运行：

```powershell
# 工程根：CLI 和 Agent
go run ./apps/cli
go run ./apps/agent

# 工程根：输出路径统一到 dist
go build -o dist/cli/hopsend-cli.exe ./apps/cli
go build -o dist/agent/hopsend-agent.exe ./apps/agent

# 桌面端：Wails 配置必须位于当前目录
Set-Location apps/desktop
wails dev
wails build
```

桌面前端目录：apps/desktop/frontend；在那里使用固定版本 pnpm，提交 pnpm-lock.yaml。
Wails build 先产生该工具约定的产物，scripts/build 后续将可分发文件归集到根 dist/desktop；不能把未核对版本的构建参数写死。

## 脚本命名预留

```text
scripts/dev/doctor.ps1            只检测开发工具与依赖状态
scripts/dev/run-agent.ps1         前台运行本机测试 Agent
scripts/dev/run-desktop.ps1       进入正确目录启动 Wails
scripts/build/build-all.ps1       构建三个程序，归集 dist
scripts/check/check-all.ps1       格式、静态检查、契约与相关测试
```

目前仅保留脚本目录；未创建假成功的脚本。未来脚本应定位自身所在仓库，检查依赖并返回明确退出码。

## 依赖与源码管理

- go.mod/go.sum 与 pnpm-lock.yaml 纳入版本管理；缓存使用工具默认缓存位置。
- 不提交 node_modules、编译结果、Wails 自动绑定、真实 DB/日志/下载内容。
- docs 和 assets 随源码提交；构建产物放 dist 并打包发布。
- 源码按用户指定放在 OneDrive 桌面；进程数据库、缓存和密钥放运行数据分区，避免将活跃运行状态当源码同步。

## 当前可执行的 S0 检查

已实现 `scripts/check/check-baseline.ps1`，需要 Git for Windows 和 PowerShell 7，用于目录、文档、参数与 Git 钩子验证：

```powershell
pwsh -NoProfile -File scripts/check/check-baseline.ps1 -ReportPath work/s0-validation.json
```

它只验证工程基线，不代替后续 Go 构建、协议联调或实际传输验收。
