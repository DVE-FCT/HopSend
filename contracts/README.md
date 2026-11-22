# 契约分区

- control/v1：已提供 Agent HTTPS API 的 openapi.json 与 Go 载荷类型；包含健康、能力、共享列表、目录查询和 P1.1 已发布清单查询。
- manifest/v1：已提供内容清单 JSON Schema 与 Go 类型，明确哈希、版本和路径；跨字段和内容哈希关系由客户端额外验证。
- events/v1：引擎到桌面的聚合事件契约。

events/v1 仍为预留。契约和实现同步演进，不能用空 schema 声称兼容性已验证。上传路由待相应阶段实现后再加入公开契约。
