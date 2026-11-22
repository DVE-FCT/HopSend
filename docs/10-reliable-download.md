# P1 可靠下载：内容清单、分块与校验

## 当前 P1.1 / P1.2 能力

- 源端将普通文件复制成独立内容快照，按完整文件 SHA-256 寻址；不会通过硬链接复用可变源文件。
- 清单包含共享 ID、Windows 安全单级文件名、64 位文件尺寸、逻辑块尺寸、连续块范围及 SHA-256。
- manifest_id 对清单结构规范 JSON 求 SHA-256；content_id 是完整文件 SHA-256，两者用途不同。
- 客户端从已配对 Agent 的受认证 HTTPS 获取清单，经主机校验和密钥认证的 SFTP 读取内容快照。
- 默认两个 worker，每个 worker 自有可取消连接；流式写入同一暂存文件的不重叠范围。
- 每块长度和哈希通过后同步文件，再报告已确认进度；全部块通过后重新读取本地完整文件核验。
- Windows 使用同卷、禁止覆盖的 MoveFileEx 发布正式文件；同名文件及并发创建的目标不会被覆盖。

P1.2 已接入 SQLite 任务与块确认记录。相同数据库、manifest 和目标路径会恢复原任务；恢复先重读已确认块，损坏块重新下载。显式暂停、自动网络退避重试属于 P1.3，当前失败后需手动运行 resume。

## 1. 源端初始化

在源设备的专用账号下，以已有可执行文件为例：

```powershell
./hopsend-cli.exe agent-init --dir ./identity --share-dir 'D:\Share' `
  --hosts 'localhost,127.0.0.1,实际Tailscale地址'
```

`--hosts` 必须填写实际访问地址。初始化目录需为空。修改 identity/agent.local.json 的 listen 为实际 Tailscale 地址加端口；按最小权限放行该源地址的 TCP 端口。

新配置有 `content_directory: "content"`，相对于配置文件目录，专门保存不可变版本；不把普通源目录直接当快照目录。
旧 P0 配置可新增该字段并创建目录，但所有现有字段和授权关系应保留。

## 2. 发布文件版本

在源端执行：

```powershell
./hopsend-cli.exe publish --source 'D:\Share\large.bin' `
  --content-dir './identity/content' --share files --chunk-mib 32
./hopsend-agent.exe --config ./identity/agent.local.json
```

记下输出的 manifest_id。发布需要足够空间同时保存源文件和独立快照；内容变更重新发布新版本。
同样内容会复用已核验的对象，但不会复用损坏缓存。发布过程检测到源文件变化时失败，清单不会发布。

```text
identity/content/
  objects/<完整文件SHA-256>       不可变快照，只读属性
  manifests/<清单ID>.json        清单
```

只读属性防止误写，不构成对拥有该文件管理权限的恶意源端的防护；客户端始终验证可信清单中的分块和全文件哈希。
私钥、API 凭据、配置及内容目录留在各自运行数据区，不进源码仓库。

## 3. 从另一台电脑下载

```powershell
./hopsend-cli.exe download `
  --url 'https://源设备Tailscale地址:9443' `
  --ca-cert '配对CA公钥文件' --token-file '本设备凭据文件' `
  --share files --manifest '源端输出的64位清单ID' `
  --address '源设备Tailscale地址:22' --user '专用账号' `
  --key '本机SSH私钥文件' --known-hosts '已核对的主机记录' `
  --output 'D:\Downloads\large.bin' --workers 2 --read-timeout 2m
```

stderr 输出状态、已校验字节、总字节和块数。stdout 最终返回目标路径、尺寸、清单 ID 与完整 SHA-256。
`--quiet` 只关闭分块进度，不关闭失败结果。P1.3 起，`--read-timeout` 表示读取期间持续无数据进展的时长；持续流入数据的慢速分块不会因为总时长超过两分钟而被中断。

Agent 与 SSH 是不同的认证；CA/API 凭据不替代 SSH 私钥及服务器指纹。首次主机信任仍需人工核对。

## 4. 查看任务和退出后续传

Windows 上 download 默认使用 `%LOCALAPPDATA%\HopSend\cli\state.db`；可用 `--database 'D:\HopSendData\tasks.sqlite'` 指定。数据库和相邻的 WAL/SHM 文件都是本机运行数据，不能提交源码，也不要在运行中分别复制。

```powershell
./hopsend-cli.exe tasks --database 'D:\HopSendData\tasks.sqlite'
./hopsend-cli.exe resume --database 'D:\HopSendData\tasks.sqlite' --task '列表返回的64位任务ID'
```

恢复保存的地址、共享和原 manifest；重新验证 HTTPS 凭据、证书与版本，再检查暂存块。重跑相同 download 参数也会恢复。更新配对凭据路径时可重跑 download；不会把不同 manifest 当作旧版本续传。

终止进程后列表中的 downloading 等状态表示最后记录，不能证明进程仍存活；恢复时重新取得目标锁、重核验数据并更新。缺失或损坏暂存块会重新入队，不会因为文件长度已预分配而误记完成。stdout 的 reused_chunks 是本次重核验后复用的块数。

暂存文件和锁标记位于目标同目录。锁标记文件在结束后保留但不持有句柄；不要通过删除锁标记绕过运行中的写入者。移动数据库、下载目录或凭据路径前应停止任务并保存备份；当前不支持任意迁移暂存路径。

分块确认顺序与发布对账见 [ADR 0003](adr/0003-durable-download-journal.md)。API 凭据和私钥内容不写入数据库；本地连接地址、用户名和文件路径仍属于运行资料。

## 控制接口与规则

```text
GET /v1/shares/{share_id}/manifests/{manifest_id}
```

只对当前凭据获准访问且清单中 share_id 匹配的版本返回数据；不提供客户端任意系统路径读取接口。
响应提供可信 manifest 与其对应的 SFTP 内容路径。客户端核对请求版本、结构、偏移连续性、长度、标识和文件名。
未知/不授权/损坏版本返回 404，未通过凭据返回 401。

逻辑块当前允许 64 KiB～64 MiB，CLI 发布使用 1～64 MiB；默认 32 MiB。最多 65,536 块，清单 JSON 上限 16 MiB。
分块不是实体分卷。SFTP 范围读取使用有界 1 MiB 缓冲和协议流水线，不将整个大文件或整个逻辑块载入内存。

## 验证层次

- 单元：空文件、末块、清单篡改、源/快照独立、损坏缓存、块和全文件错误、取消、目标竞争。
- 本机集成：真实 HTTPS 清单 + 真实 SSH/SFTP 的两个 worker + 实际磁盘文件。
- 两设备：独立 HTTPS 端口、Windows OpenSSH、超过 4 GiB 的真实文件与源端/本地独立哈希。
- 持久化、进程终止、断网、坏块恢复及不同宽带将在 P1.2/P1.3 留独立证据。
