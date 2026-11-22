# 运行数据、配置与源码分区

## 1. 源码目录

`C:\Users\Lenovo\OneDrive\Desktop\HopSend` 保存代码、契约、脱敏配置示例、文档、品牌和打包源材料。
真实传输文件、SQLite、日志、凭据、机器配置与缓存不作为工程内容提交。

## 2. 桌面端和 CLI 的用户数据

建议由程序解析 Windows 标准目录，以下为规划相对布局：

```text
%APPDATA%\HopSend\
  config.yaml                 非机密用户配置
  trust\                      节点/主机公钥信任记录，不含私钥

%LOCALAPPDATA%\HopSend\
  desktop\state.db            桌面任务库
  cli\state.db                CLI 独立任务库
  logs\                      按进程角色分文件
  cache\manifests\            可重建清单缓存
  work\                      可恢复任务的受控中间记录
```

P1.2 的 CLI 默认使用上述 `cli\state.db`，桌面端仍待实现，起步使用独立数据库。显式使用同一数据库时，SQLite 负责状态事务，目标内核锁拒绝两个进程同时写同一目标；不能把共享数据库当作跨进程任务控制通道。未来共享活动任务需要独立引擎和 IPC。

## 3. Agent 服务数据

```text
%PROGRAMDATA%\HopSend\
  agent\config.yaml
  agent\state.db
  agent\logs\
  agent\trust\
```

上述目录需在服务安装时配置最小权限，不能假设普通进程可以任意写入 ProgramData。
前台开发 Agent 使用显式 --config/--data-dir 指定测试目录；命令参数在实现时固定。

## 4. 用户文件与暂存

用户可选择下载目录，例如 `%USERPROFILE%\Downloads\HopSend`。
暂存文件与最终文件放在同一目标卷，按受控任务 ID 创建唯一暂存名，避免同名覆盖和跨卷重命名。
续传状态根据数据库与 manifest 对应，不能从暂存文件长度判断完成。

源目录与上传目录均为用户/管理员明确授权的路径，Agent 配置使用 share_id 对外表示，不公开任意系统路径。

## 5. 凭据与配置

- 私钥或 Token 使用 Windows 受保护凭据机制，或用户选择的受 ACL 保护路径。
- 配置和 SQLite 只保存 credential_ref，不记录真实密码或私钥。
- SSH 主机公钥信任与私钥分开保存；证书验证失败时停止，不能默认跳过。
- `configs/examples/design-defaults.json` 是设计参数清单，不是已实现的程序配置格式。
- 后续 desktop/agent/cli 配置 schema 各自定义角色字段，共享配置模块提供解析与验证。

## 6. 构建与发布输出

```text
HopSend/dist/
  desktop/                    桌面可执行文件
  agent/                      Agent 可执行文件
  cli/                        CLI 可执行文件
  packages/                   安装包与便携归档
  checksums.sha256             真实产物生成后计算
```

dist 在构建时生成并已忽略。文件哈希必须对实际发布产物计算，不提前写占位哈希。
