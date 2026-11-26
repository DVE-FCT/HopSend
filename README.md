# 跃传 HopSend

跨设备文件传输助手，面向 Windows 和可信协作设备。

**当前状态：可靠下载、Windows 桌面、接收端确认上传和目录包传输已实现。支持分块续传、任务控制、网络重试和 SHA-256。已通过两设备 5 GiB＋17 字节恢复、热点续传、真实桌面收发，以及目录/空目录的两设备往返。已加入分层网络诊断、真实速度/ETA、持久活动记录、任务筛选和明暗主题。P2.5 补充已配对设备的连接前自动部署、启动与网络验收。**

工程路径：`C:\Users\Lenovo\OneDrive\Desktop\HopSend`。

P2.6 优化主题、任务反馈、设备观察与编辑抽屉、日志跳转和下载完成定位；上传支持拖入选择，原生拖放尚待现场验收，具体实测与限制见 [P2.6 验收](docs/validation/P2.6.md)。

P2.9 为 Agent 和 CLI 补充目录分页：默认 100、最大 500 项/页，CLI `agent-status --share files --all` 可自动遍历。分页期间检测到目录变化会提示重新读取，支持超过 1000 项的目录；协议、使用方法和限制见 [目录分页验收](docs/validation/P2.9-directory-pagination.md)及 [ADR 0010](docs/adr/0010-directory-pagination.md)。本阶段未新增桌面目录浏览界面。

P2.10 将发送端配对资料业务迁入 Go：新增 `pair request/inspect/import`，原生生成 SSH 密钥、校验回应并固定本地信任；使用方法见 [Go 配对资料核心](docs/21-native-pairing-materials.md)。接收端部署和在线桌面预览仍按 P2.8 单独验收。

P2.11 新增[桌面四步配对向导](docs/22-desktop-pairing-wizard.md)：确认本机地址、生成公开请求、选择/拖入回应、核对指纹后保存设备。P2.12 保存后自动验收连接，失败保留资料并可跳转诊断；增加受限手动地址回退和每 30 秒只读健康观察。无需手填八个连接字段。源设备图形准备在独立 P2.8 预览包中继续现场验收。

P2.13 支持设备卡片中的“轮换信任”：独立核对新 SSH/CA 指纹后切换凭据版本，保留设备 ID、旧凭据和任务历史。旧任务仍固定原授权，不会自动接受新身份；见[轮换验收与边界](docs/validation/P2.13-trust-rotation.md)。

P2.14 整理界面视觉与文案、Git 工作区及阶段规划。P2.15/16 已补目录浏览、显式发布及路径版本下载；P2.17 增加[上传目的子目录](docs/26-upload-destination.md)。P2.18 增加[一发多收](docs/27-batch-upload.md)，多源仍按[后续实施计划](docs/23-next-stage-plan.md)推进。P2.8 的干净系统与源端现场准备单独验收，P3.x 合并须先关闭这一前提。

P2.15 新增[共享目录浏览](docs/24-remote-directory-browser.md)。P2.16 补齐[源端显式发布和点选下载](docs/25-published-path-download.md)：源设备在“设备与信任 → 发布本机内容”发布快照；下载设备在目录中检视已发布版本，点击“下载到…”选保存位置后建任务。未发布有指引，保留手工 ID；下载和恢复固定所选版本，不因源文件后来变化而切换。

P2.17 在上传页选择共享内已有父目录，只写共享仍只允许手工输入路径。恢复与发布核验目录身份；SQLite 升级前自动保留一致备份，缺少绑定的旧上传会话需核对后以新目标重建。使用和兼容边界见[上传子目录说明](docs/26-upload-destination.md)。

P2.18 在上传页多选已配对接收设备，各自设置共享与父目录。一次生成固定快照，各台独立确认、进度和暂停/继续/取消；关闭重开后手动继续。失败不牵连其他接收端。SQLite v4 升级备份及容量限制见[批次说明](docs/27-batch-upload.md)。

## 名称与输出

| 项目 | 约定 |
|---|---|
| 产品中文名 | 跃传 |
| 产品英文名 / 工程目录 | HopSend |
| 源码标识 / CLI 命令 | hopsend |
| 桌面主程序 | hopsend.exe |
| 节点服务程序 | hopsend-agent.exe |
| 诊断与自动化程序 | hopsend-cli.exe |
| Windows 服务名 | HopSendAgent |
| Windows 服务显示名 | HopSend Agent |
| 根 Go 模块 | github.com/DVE-FCT/HopSend |
| API / 清单契约 | v1，与应用发行版本分别管理 |

代码仓库：[DVE-FCT/HopSend](https://github.com/DVE-FCT/HopSend)，SSH 地址为 `git@github.com:DVE-FCT/HopSend.git`。沿用远端初始化时选择的 [MIT 许可证](LICENSE)。已有 `DVE-FCT/DB` 用于数据库资料下载。

## 阶段与提交规范

完成并验证一个阶段后，才提交到对应的 `test/<阶段>-<主题>` 分支；提交标题统一使用中文，例如 `文档(S0): 建立工程基线与阶段验收规范`。
通过 PR 集成最新 main、解决冲突并重新验证后，再合入主分支；不得直接推送 main。详见[Git 工作流](docs/07-git-workflow.md)及[阶段验收记录](docs/validation/README.md)。

## 目录总览

```text
HopSend/
├── apps/                  desktop、agent、cli 三个程序入口
├── internal/              三个程序共享的 Go 业务内核及平台适配
├── contracts/             API、清单和 UI 事件的版本化契约
├── configs/examples/      无凭据的配置示例
├── assets/                品牌图标和系统结构图
├── scripts/               开发、构建和检查脚本
├── tests/                 集成、端到端、样例和性能验证
├── packaging/windows/     Windows 安装包与后台服务材料
├── docs/                  工程规划与架构决策
├── AGENTS.md              后续代码协作规则
├── .gitignore
├── .gitattributes
└── .editorconfig
```

## 文档入口

1. [工程命名和文件分区](docs/01-project-layout.md)：每个目录放什么、哪些文件后续创建。
2. [系统架构和依赖边界](docs/02-architecture.md)：控制面、数据面和调用约束。
3. [协议与存储规划](docs/03-contracts-storage.md)：API、任务、分块和文件版本。
4. [开发阶段与验收](docs/04-roadmap.md)：从连通验证到多节点的实施顺序。
5. [开发环境与构建](docs/05-development.md)：环境要求和未来命令。
6. [运行数据与配置分区](docs/06-runtime-layout.md)：数据库、日志、凭据和下载目录。
7. [Git 工作流与中文提交规范](docs/07-git-workflow.md)：测试分支、PR、冲突解决和合并验证。
8. [P0 当前使用方法](docs/08-p0-usage.md)与[两设备现场验收](docs/09-p0-device-runbook.md)。
9. [两台 Windows 实测记录](docs/validation/P0.2-device.md)：包含真实联调发现的主机密钥算法选择修复及验证范围。
10. [P1 可靠下载使用方法](docs/10-reliable-download.md)与[P1～P2 完整验收矩阵](docs/11-p1-p2-acceptance.md)。
11. [任务控制与网络恢复](docs/12-task-recovery-controls.md)：暂停、继续、取消、错误分类与有界重试。
12. [Windows 桌面使用方法](docs/13-desktop-usage.md)：实际构建、设备配对引用与任务操作。
13. [接收端确认上传](docs/14-confirmed-upload.md)：独立写入授权、CLI/桌面、接收端确认和发布。
14. [文件夹与路径](docs/15-folder-transfer.md)：目录包、安全展开、空目录、长路径与缓存保留。
15. [诊断、监控与界面](docs/16-diagnostics-monitoring.md)：分层检查、真实字节口径、速度/ETA、持久记录及主题。
16. [连接前设备准备](docs/17-device-preparation.md)：自动部署/启动、HTTPS/SFTP 实测及首次信任边界。
17. [单源性能基线](docs/18-single-source-benchmark.md)：有界的真实样本、SFTP/引擎对照、并发矩阵及参数依据。

## 技术基线

Wails + Vue 3 + TypeScript，Go 引擎，SQLite，HTTPS/JSON Agent，Windows OpenSSH/SFTP，Tailscale/WireGuard，SHA-256。

一个仓库、根目录一个 Go 模块。Wails 项目根目录是 `apps/desktop`，其前端固定放在 `apps/desktop/frontend`。
所有 Go 入口共用根 `go.mod`；不为每个入口创建独立模块，不使用 `go.work`。

默认规划参数：32 MiB 逻辑块、每节点 2 个活动块。P2.7 已完成两设备直连矩阵实测，保留这组参数为当前保守起点，完整结果包含其他组合更快的条件；全局 4 个活动块仍待多源验证，见[单源性能基线](docs/18-single-source-benchmark.md)。

P2.7 已完成，P2.8 可运行分发/用户登录驻留在本地预览中实现，仍待整阶段现场关闭。目录浏览等独立客户端功能可继续推进；P3 的物理多源与发布验收需要稳定的源节点，P4.1 紧接调度完成开展性能回归与发布边界注入。当前 EXE 运行不依赖 Go/Node；机器级无人值守服务、正式安装器与跨平台支持尚未交付，详见[阶段规划](docs/04-roadmap.md)。

## 当前可运行命令

工具链版本见 `configs/toolchain.json`：Go 1.27.4、Node 24.14.1、pnpm 11.25.0、Wails 2.17.1。

```powershell
# 在工程根目录，准备本地 Go/Wails；需要系统代理时追加 -UseSystemProxy
./scripts/dev/bootstrap-tools.ps1
. ./scripts/dev/env.ps1
go run ./apps/cli version
go run ./apps/cli doctor --strict
./scripts/check/check-go.ps1 -StrictDoctor
```

`doctor` 检查开发工具版本，不代表远端设备、WebView2、桌面构建或传输可用。
Node 和 pnpm 需另外安装到 PATH。引导脚本只写项目内忽略目录 `work/.tools`，不修改机器 PATH。

## HTTPS Agent 本机启动

```powershell
. ./scripts/dev/env.ps1
go run ./apps/cli agent-init --dir work/local-agent
go run ./apps/agent --config work/local-agent/agent.local.json
```

在另一终端执行（默认不共享目录）：

```powershell
. ./scripts/dev/env.ps1
go run ./apps/cli agent-status --url https://localhost:9443 `
  --ca-cert work/local-agent/ca.crt --token-file work/local-agent/client.token
```

要授权目录，在首次初始化时追加 `--share-dir "实际共享路径"`，查询时追加 `--share files`。已有配置目录不可重复初始化。
仅将 CA 公钥及对应设备凭据安全交给客户端，不传送 server.key；Windows 私密文件 ACL 需另行核对。
