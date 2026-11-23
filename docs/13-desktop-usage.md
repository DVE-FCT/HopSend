# Windows 桌面端

## 构建和启动

在工程根目录执行：

```powershell
. ./scripts/dev/env.ps1
./scripts/build/build-desktop.ps1
./dist/desktop/hopsend.exe
```

Wails 项目根为 apps/desktop，根目录只有一份 go.mod。构建先安装锁定的 pnpm 依赖，运行 vue-tsc 与 Vite，再由固定 Wails CLI 打包实际 Windows 程序。
需要已经安装 WebView2；`wails doctor` 可以检查运行依赖。命令不会安装服务或修改防火墙。
开发时在 apps/desktop 执行 `wails dev`。Go 内核检查器会先准备前端嵌入资源；CI 同时构建 CLI、Agent 和实际 Wails 可执行文件。

## 设备与信任

添加设备时填入已配对 Agent HTTPS 地址、SSH 地址/账号、共享 ID，以及 CA 公钥、API 凭据、私钥和已核对主机记录的本机文件路径。
点击检查连接时会实际验证 HTTPS 与 SSH，不是仅检查文本格式。首次信任建立仍需按两设备配对流程完成；桌面端不会自动接受未知证书或主机密钥。

也可以导入只包含连接信息和文件引用的 JSON，然后核对并保存：

```json
{
  "name":"工作电脑",
  "agent_url":"https://源设备地址:9443",
  "ssh_address":"源设备地址:22",
  "ssh_user":"专用账号",
  "share_id":"files",
  "ca_file":"D:/HopSendTrust/ca.crt",
  "token_file":"D:/HopSendTrust/client.token",
  "key_file":"D:/HopSendTrust/id_ed25519",
  "known_hosts_file":"D:/HopSendTrust/known_hosts"
}
```

不要把凭据正文放入 JSON；软件保存引用，不替代原凭据文件的 ACL。SSH 文件权限与 API 共享授权分别生效。

## 真实下载与恢复

选择设备，填写源端发布的 manifest_id，选择正式文件保存路径后开始下载。根据清单实际块尺寸调度，不用界面固定值覆盖原清单。
进度来自引擎已校验、同步并确认的字节；校验与发布仍有独立状态。记录和进度均为实际任务数据。

暂停等待 worker 退出并释放写入所有权。关闭窗口会先暂停剩余任务；重开后可以从本地数据库继续，重读核验已有块后再传输。
取消只取消任务并保留暂存数据，不会删除用户文件；已取消任务不会由普通继续按钮重新启动。

桌面任务库和设备引用配置保存在 `%LOCALAPPDATA%/HopSend/desktop/`，与 CLI 默认任务库分开。测试可用进程环境变量 HOPSEND_DESKTOP_DATA 指定独立目录。
连接尚未取得清单时显示本次会话的连接任务；只有取得可信原 manifest 后才创建持久化下载，不能把纯连接失败的临时记录当作已下载任务恢复。

## 界面与能力范围

当前包括设备配置、下载/确认上传、文件夹收发、真实任务列表及筛选、暂停/继续/取消、关闭重开恢复、持久任务状态记录、分层网络诊断和明暗主题。
界面每 750 毫秒读取共享应用层状态；Go 同时发布 task:progress 事件，载荷见 contracts/events/v1/events.schema.json。列表初次读取会将上次异常退出的活动状态标为可恢复，不把数据库旧状态当成仍有 worker 存活。

确认上传见 [上传说明](14-confirmed-upload.md)，文件夹见 [路径说明](15-folder-transfer.md)，速度口径、持久记录与诊断见 [诊断监控](16-diagnostics-monitoring.md)。
