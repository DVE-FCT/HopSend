# 契约分区

- control/v1：已提供 P0.3 Agent HTTPS API 的 openapi.json 与 Go 载荷类型；目前只有健康、能力、共享列表和目录查询。
- manifest/v1：内容清单 JSON Schema，明确哈希、版本和路径。
- events/v1：引擎到桌面的聚合事件契约。

manifest/v1 和 events/v1 仍为预留。契约和实现同步演进，不能用空 schema 声称兼容性已验证。上传和 manifest 路由待相应阶段实现后再加入公开契约。
