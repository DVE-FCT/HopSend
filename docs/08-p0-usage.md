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
