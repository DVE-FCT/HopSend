# 连接前的设备准备与自动恢复

## 使用流程

新设备第一次需要完成一次基础安装与信任配对：Tailscale 登录并授权，Windows OpenSSH/SFTP 启用，专用账号公钥、主机指纹、Agent 的 HTTPS 证书/凭据和共享权限核对。涉及管理员权限与设备授权的首次步骤仍由设备所有者执行，方法见 [两设备配对](09-p0-device-runbook.md)。本阶段不把这些步骤描述为已具备通用图形安装器。

配对信息导入桌面后，在“设备与信任 → 编辑”填写首次配对生成的远端 Agent 配置绝对路径，启用“连接时自动准备远端 Agent”。保存后立即准备并验收；以后直接点击下载、上传或诊断，软件会先自动准备，不需要每次去远端手工运行 Agent。

现有手动管理设备仍兼容；未启用自动准备时不会擅自部署远端程序。配对 JSON 新增可选字段：

```json
{
  "manage_agent": true,
  "remote_agent_config": "C:/Users/专用账号/AppData/Local/HopSend/identity/agent.local.json"
}
```

这些字段补充到原有设备 JSON，不能替代 HTTPS/SSH 文件引用。软件不自动接受未知证书或 SSH 主机密钥，不更换 API 凭据或扩大原共享权限。

## 自动准备顺序

1. 验证 SSH 主机密钥、公钥身份和 SFTP 子系统。
2. 检查远端 Windows x64 与原 Agent 配置，创建专用账号用户目录内的托管程序区。
3. 已有 Agent 可通过 HTTPS 时复用；不可用时上传本机配套 Agent，在随机暂存路径完成后验证 SHA-256，再禁止覆盖地发布到按哈希命名的版本目录。
4. 从认证 SSH 会话启动 Agent，等待独立 HTTPS 就绪；验证证书、API 凭据和所选共享。信任或授权错误直接失败，不以重部署绕过。
5. 通过 SFTP 在专用探测目录写入并读回随机 32 KiB，核对长度和 SHA-256，再清理测试文件。
6. 只有上述步骤全部通过才显示“设备已就绪”，随后进入实际任务或完整诊断。

准备过程会产生实际网络流量和账号目录内的程序缓存。不会自动安装系统服务、更改防火墙或 SSH 全局设置。标准账号无权完成的首次系统安装与防火墙授权仍有明确边界。

## 程序与生命周期

`scripts/build/build-desktop.ps1` 同时生成 `dist/desktop/hopsend.exe` 和 `hopsend-agent.exe`。复制软件到其他目录时保留这两个程序；完整预览包还包含 CLI。健康 Agent 可直接复用；需要部署但找不到配套程序时，界面会说明缺失原因。

Windows OpenSSH 管理其子进程生命周期，不能把 `Start-Process` 返回 PID 当成远端长期可用的证据。桌面持有 Agent 的认证 SSH 执行会话，并在之后的连接或传输重试前检查健康状态，缺失时重新准备。关闭客户端会释放管理连接；远端进程是否立即结束由 Windows OpenSSH 决定，下次连接始终重新检查，不依赖遗留进程。

这实现了客户端使用期间的按需恢复，不是开机自启的 Windows 服务。后续 P2.8 先验证用户登录计划任务驻留，机器级无人值守服务仍属 P4.2；两者均未在 P2.6 实现。

远端程序区：`%LOCALAPPDATA%/HopSend/Agent/managed/versions/<sha256>/hopsend-agent.exe`；探测文件位于同级 probes 目录。部署中断可能留下随机 `.tmp` 文件，不会将半个程序标记为完整版本；程序缓存暂未自动回收。

## CLI 准备验收

```powershell
./dist/cli/hopsend-cli.exe device-prepare `
  --device-config D:/HopSendTrust/device.json `
  --agent-binary ./dist/desktop/hopsend-agent.exe
```

返回分步报告；失败返回非零退出码。CLI 退出释放管理连接，不作为长期驻留服务使用。日常桌面流程会自行持有连接并执行后续恢复。
