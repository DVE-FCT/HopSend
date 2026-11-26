# 上传到接收子目录

P2.17 支持选择共享内已经存在的父目录，目标名称仍是单级文件名。系统不会替用户创建远程目录，也不会把不支持的子目录上传悄悄改到共享根。

## 桌面操作

1. 在“传输任务 → 上传到设备”选择接收设备和共享 ID。
2. “接收父目录”留空表示共享根。读写共享可展开“选择接收目录”，选择授权共享、进入子目录，点击“使用此目录”。只有上传权限的共享不显示目录树，请手工填写共享内相对路径，例如 `项目/资料`；这不会提升读取权限。
3. 选择本机文件/文件夹，可修改接收文件名。核对共享、父目录和名称后，点击“开始上传”。
4. 等待接收端确认块及完整 SHA-256，最终状态完成后，文件才位于选定父目录。已有同名目标不会覆盖。更换设备或共享会清空旧父目录选择。

目录须已存在；绝对路径、`..`、反斜线、ADS、链接/重解析点和 `.hopsend-uploads` 临时区均拒绝。当前版本不提供远程新建目录按钮。

## CLI

在现有 `upload` 命令的连接/凭据参数基础上追加：

```powershell
hopsend-cli.exe upload --url https://your-node:9443 --ca-cert C:\HopSend\ca.crt --token-file C:\HopSend\client.token --address your-node:22 --user hopsend --key C:\HopSend\id_ed25519 --known-hosts C:\HopSend\known_hosts --share files --source C:\Send\report.pdf --target report.pdf --parent-path "项目/资料"
```

`--parent-path` 缺省/空串/`.` 都指共享根。接收 Agent 必须声明 `upload_parent_path` 才能上传到非根；不支持时在复制本机快照前拒绝，根目录兼容入口保留。

## 恢复、目录变化和升级

任务身份包括父目录，SQLite 会话固定共享根和父目录的文件系统身份；暂停、重启后继续使用同一目的目录。父目录在传输期间删除、替换或变成链接，状态/确认/finalize 会拒绝，不能继续写入新的同名目录。暂存始终留在共享根内受控区域，SFTP 不经过可变化的目标父路径；最终发布只在核验后发生。Windows 控制请求持有目录链的禁止重命名句柄，释放后用户可正常操作目录。

任务库与 Agent 状态库升级到 SQLite schema v3。v1/v2 数据库先用 `VACUUM INTO` 生成同目录 `.pre-v3-*.bak` 一致备份，再迁移，原记录保留。旧上传会话没有父目录身份，返回 `upload_parent_unbound`；须在接收端核对后用新目标重建任务，不能自动冒认。新根目录上传仍与旧 wire 形状一致；旧软件不能读取 v3 数据库。回退时先停止相关桌面/Agent，再保留现状态并恢复匹配版本的升级前备份，勿在运行时直接复制数据库覆盖。

设计见 [ADR 0015](adr/0015-upload-parent-binding.md)，实际验证见 [P2.17](validation/P2.17-upload-destination.md)。
