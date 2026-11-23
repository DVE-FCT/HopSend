# 单源性能基线

## 范围与口径

`hopsend-cli benchmark` 顺序执行本地 SHA-256、源读取＋新文件写入＋按块 Sync、单 SSH 连接 SFTP 流式哈希，以及生产下载引擎（HTTPS 清单、每 worker 独立 SSH、块校验、Sync、SQLite、最终完整校验与发布）。引擎计时包含必需的完整文件校验；额外独立目标重读核验不计入耗时。源文件限定为合成常规文件，1 字节至 4 GiB。

SFTP 基线含建连与流式哈希，不包含目标写盘/SQLite，不能称为纯网络链路极限。每次网络读取均核对完整 SHA-256；有效载荷按 SFTP 实际字节计量，不含 SSH/IP 协议开销。新的任务数据库与目标文件防止续传复用使结果虚高。网络参数矩阵顺序在重复轮次间轮换，减少固定顺序偏差。

不清理操作系统缓存、不修改网络设置。首次本地读取的缓存状态为未知；之后本地源为热缓存。网络矩阵先做一次完整且校验通过的 SFTP 预热，此后使用热源和新目标。不能将这些结果描述成冷盘或无缓存测量。

客户端进程 CPU 用 Windows `GetProcessTimes` 差值，工作集与 Go 堆每 50ms 采样；短峰值可能漏采。不是整机/远端资源监测，不拿多个样本累积最大工作集冒充本次峰值。原始样本和 min/median/max 一起保存，保留故障轮次，失败不能混入成功统计。

## 可执行步骤

在本机及源端准备专用测试目录。源端用相同种子和参数生成文件，并分别核对 SHA-256；跨 .NET 版本要核对哈希，不能只假设随机数序列相同。

```powershell
./scripts/perf/new-fixture.ps1 -Path D:/HopSendBench/source.bin -SizeMiB 192 -TailBytes 17
```

源端在 Agent 配置实际 `content_directory` 下分别发布 8、32、64 MiB 清单，三份清单必须具有相同内容哈希及长度。相对内容目录是相对于配置文件所在目录。

```powershell
./hopsend-cli.exe publish --source D:/HopSendBench/source.bin `
  --content-dir D:/HopSendAgent/content --share files --chunk-mib 32
```

将真实清单 ID 按下面示例组成计划；`workers` 允许 1/2/4，最多 27 个不同组合，重复 1–10 次。完整 3×3×3 矩阵另有 1 次 SFTP 预热和 3 次单连接对照，192 MiB＋17 字节样本预计有效载荷为 6,241,124,879 字节（约 5.81 GiB），协议开销及失败重跑另计。预算不足会在网络测量前拒绝执行。

```json
{
  "label": "填写设备/磁盘/网络环境，不写账号或私钥信息",
  "local_source": "D:/HopSendBench/source.bin",
  "repeats": 3,
  "sync_chunk_mib": 32,
  "traffic_budget_bytes": 10737418240,
  "cases": [
    {"manifest_id": "填入实际的64位清单ID", "workers": 1},
    {"manifest_id": "填入实际的64位清单ID", "workers": 2},
    {"manifest_id": "填入实际的64位清单ID", "workers": 4}
  ]
}
```

以上清单 ID 是占位文字，执行前替换；完整矩阵对三种块尺寸各列三行。设备 JSON 沿用桌面配对文件引用格式。基准只读现有 Agent，不自动部署或启动远端程序，先从桌面“准备并验收”确认就绪。

```powershell
./hopsend-cli.exe benchmark --plan D:/HopSendBench/plan.json `
  --device-config D:/HopSendTrust/device.json `
  --output D:/HopSendBench/run-001 --timeout 2h
```

输出目录必须不存在且父目录已存在。每个样本结束后保存 `report.json`，最终 `complete=true` 才是一组完成测量。SFTP 对照每次最多 10 分钟，整组最多两小时；失败保留已完成样本和错误退出码。取消/进程被强制结束时不得将未完成报告作为通过。

校验通过后的基准下载文件会清理，任务数据库仍留在专用目录且含设备引用，应只留本机。`report.json` 不序列化设备地址、凭据路径或 Token，但自填 label 仍需自行脱敏。基准只清理本次创建的文件，不覆盖源或已有输出。

## 参数结论的边界

单源结果用于比较本次已测设备上的 1/2/4 worker 和块尺寸，不证明多节点总预算最优、不保证突破共享上行。P3 必须同时约束总活动块、每节点预算、磁盘与内存；全局 4 块在多源验收前仍为设计初值。直连/中继只根据观察快照标记，网络条件发生变化时单列结果。

本次真实环境、完整样本与取值决定见 [P2.7 验收](validation/P2.7.md)。
