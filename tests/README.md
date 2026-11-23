# 验证分区

包内逻辑测试紧邻源码。此处仅保存跨组件集成、真实进程端到端、合成样例及性能实验。
不要放入数 GB 真实用户数据，测试大文件应按种子生成并用哈希核对。

S0～P2 的实际验证见 docs/validation，包括回环 SSH/SFTP、HTTPS、SQLite 与进程恢复及两设备收发。P2.7 增加生产引擎单源基准，生成器见 scripts/perf，运行方法见 docs/18-single-source-benchmark.md；原始脱敏报告放 docs/performance，机器专用计划、凭据引用与任务数据库留在忽略的 work。多源及未执行的物理故障场景不能宣称通过。
