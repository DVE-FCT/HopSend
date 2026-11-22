# 配置示例分区

当前 design-defaults.json 记录已决定的设计初值，供后续实现参考；程序尚未实现，不声明能直接加载。

未来新增 desktop.example.yaml、agent.example.yaml、cli.example.yaml 时，先确定配置 schema、路径解析和凭据引用格式。
示例不包含真实 IP、Token、密码、私钥或此电脑的个人目录。

P0.3 已提供 agent.example.json（需要先生成证书与凭据并填写真实授权目录）；推荐通过 CLI agent-init 创建实际 agent.local.json。示例中的路径只是相对于配置文件的占位目录，不能直接当作已配置服务运行。
