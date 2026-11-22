# 工程命名与文件分区

## 1. 固定命名

产品：跃传 HopSend。目录：HopSend。代码、命令、配置键使用 hopsend。

| 对象 | 约定 |
|---|---|
| 应用标识（规划） | dev.hopsend.desktop |
| 桌面输出 | hopsend.exe |
| 节点输出 | hopsend-agent.exe |
| CLI 输出 | hopsend-cli.exe |
| 服务名 | HopSendAgent |
| 环境变量前缀 | HOPSEND_ |
| 前端包名（后续创建） | @hopsend/desktop-ui |
| Go 根模块（后续创建） | github.com/DVE-FCT/HopSend |
| 软件版本 | SemVer，首次可用版本可从 0.1.0 开始 |
| 外部控制契约 | /v1；不随每次软件修复升级 |

应用标识是本地规划值，未声明拥有域名或注册商标。代码仓库已确定为 `DVE-FCT/HopSend`，Go 模块使用对应公开路径。

## 2. 三个程序入口

### apps/desktop

Wails 项目根，构建或启动 Wails 时进入此目录。后续主要文件：

```text
apps/desktop/
├── main.go              Wails 运行配置、窗口和生命周期
├── app.go               应用装配和 embed frontend/dist
├── wails.json           固定所选 Wails 稳定版的构建配置
├── bridge/              面向 UI 的方法绑定与事件适配
├── build/               Wails 平台构建源材料，生成可追踪内容后创建
└── frontend/
    ├── package.json
    ├── pnpm-lock.yaml
    ├── vite.config.ts
    ├── tsconfig.json
    ├── index.html
    ├── public/
    ├── src/{app,views,components,stores,services,types,styles}/
    ├── wailsjs/          自动生成，忽略
    ├── node_modules/     本机依赖，忽略
    └── dist/             Vite 输出，忽略
```

默认采用已选定并锁版本的 Wails 稳定主版本。如果其模板与此处的 app.go/绑定机制不同，应以实际版本调整并写 ADR，不混用多个主版本的命令。

页面建议：DevicesView、FilesView、TasksView、TaskDetailView、DiagnosticsView、SettingsView。
在页面需求确定后创建文件；前端从 services 访问 Wails，不直接触碰 SSH 私钥、数据库或 SFTP。

### apps/agent

main.go 只做参数/配置读取、服务启动、依赖装配与退出。HTTP 路由、共享和上传业务位于 internal/agent。
Windows 服务生命周期放 internal/platform/windows，既支持前台诊断也支持后续后台服务。

### apps/cli

P0 首先实现此入口，用于 doctor、节点连接验证及单文件传输。CLI 复用内部用例，不复制一套传输内核。

## 3. 共享内核职责

| 目录 | 职责 |
|---|---|
| `internal/domain` | 任务、内容、分块、节点等领域类型和错误；不依赖 UI、SQLite、SFTP。 |
| `internal/app` | 用例编排、依赖注入、状态聚合和任务命令。 |
| `internal/engine` | 任务状态机、生命周期、续传及最终发布协调。 |
| `internal/scheduler` | 分块队列、租约、节点选择及并发预算。 |
| `internal/manifest` | 内容清单、版本、哈希和清单合法性校验。 |
| `internal/transport` | 范围读取、写入和能力接口。 |
| `internal/transport/sftp` | SSH/SFTP 传输实现与连接管理。 |
| `internal/nodeclient` | 调用远端 Agent 的 HTTPS 客户端。 |
| `internal/agent/httpapi` | Agent API 路由、鉴权适配、请求与错误映射。 |
| `internal/agent/shares` | 共享目录授权、内容发布与可用性。 |
| `internal/agent/uploads` | 上传会话、接收端校验、finalize 和恢复确认。 |
| `internal/network/tailscale` | Tailscale 状态适配和诊断，能力按所用版本实现。 |
| `internal/storage/sqlite` | SQLite 仓储实现、事务和写入串行化。 |
| `internal/storage/sqlite/migrations` | 版本化 SQL 迁移，后续通过 go:embed 嵌入。 |
| `internal/filesystem` | 受控路径、暂存文件、WriteAt、Sync、空间和原子发布。 |
| `internal/security` | 主机密钥信任、凭据引用、API 授权和证书检查。 |
| `internal/telemetry` | 结构化日志、速度窗口和聚合指标。 |
| `internal/config` | 配置结构、加载、默认值和校验。 |
| `internal/platform/windows` | Windows 服务、凭据存储等平台实现。 |

## 4. 顶层保留分区

| 目录 | 管理内容 |
|---|---|
| contracts | 外部协议与事件的单一契约来源；实现代码与契约一致 |
| configs/examples | 可提交的脱敏样例；不把开发者机器配置作为默认值 |
| assets/branding | SVG 原稿、PNG 和 ICO；安装包图标从此处派生 |
| assets/diagrams | 架构图 SVG 原稿和 PNG 预览 |
| scripts | 便捷命令，不承载领域业务或真实私钥 |
| tests | 跨组件/真实进程验证、合成样例和性能实验 |
| packaging/windows | 安装包与服务管理源材料；不存编译结果 |
| docs/adr | 设计理由、取舍和变更记录 |
| dist（构建时生成） | 统一对外产物：desktop、agent、cli 及校验清单 |
| work（需要时生成） | 可清理的开发中间文件；不作为唯一配置或数据存放处 |

## 5. 文件命名规则

- Go 包名使用简短小写单词；文件为 snake_case.go，测试紧邻为 *_test.go。
- Windows 特定实现使用 *_windows.go；需要时提供其他平台的显式不支持实现。
- Vue 页面和组件使用 PascalCase.vue；TS 工具文件使用 kebab-case.ts。
- JSON/YAML 字段统一 lower_snake_case；Go 类型按惯例 PascalCase。
- SQL 迁移使用 000001_initial.up.sql 等有序命名；回滚是否支持需按数据库变更策略决定，不虚构可逆迁移。
- 任务/文件/节点/清单 ID 使用稳定的不透明标识；文件内容 ID 与 manifest ID 分开。
- 保留一个主模块和一份桌面前端锁文件；不提前引入多模块或多个前端工作区。
- 包内单元测试紧邻实现；tests/integration 不变成所有测试的堆放目录。

## 6. 当前骨架的边界

目前仅创建目录、.gitkeep、说明、样例设计参数和品牌资料。上述 main.go、go.mod、package.json、SQL、OpenAPI 等文件按相应阶段生成，不放空壳伪装为可运行实现。
后续开发先在根创建 go.mod；不能在 apps/desktop 又创建第二份 go.mod。
