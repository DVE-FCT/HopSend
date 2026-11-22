# 跃传 HopSend

跨设备文件传输助手，面向 Windows 和可信协作设备。

**当前状态：已建立工程规划、目录骨架与品牌资料；传输程序尚未实现，当前没有可运行客户端。**

工程路径：`C:\Users\Lenovo\OneDrive\Desktop\HopSend`。

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
| 根 Go 模块（后续初始化） | github.com/DVE-FCT/HopSend |
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

## 技术基线

Wails + Vue 3 + TypeScript，Go 引擎，SQLite，HTTPS/JSON Agent，Windows OpenSSH/SFTP，Tailscale/WireGuard，SHA-256。

一个仓库、根目录一个 Go 模块。Wails 项目根目录是 `apps/desktop`，其前端固定放在 `apps/desktop/frontend`。
所有 Go 入口共用根 `go.mod`；不为每个入口创建独立模块，不使用 `go.work`。

默认规划参数：32 MiB 逻辑块、每节点 2 个活动块、全局 4 个活动块；这些是待实测的初始值。

## 开始开发前

当前检查时未在 PATH 中检测到 Go 和 Wails；已检测到 Node.js、npm/pnpm 和 Git，但其版本兼容性尚未验证。
下一步按开发环境文档选择并固定受支持版本，创建根 `go.mod`、CLI 入口和连通验证。
本骨架没有写入虚构版本号、依赖清单或无法启动的示例程序。
