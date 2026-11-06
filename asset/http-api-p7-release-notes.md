# HTTP API P7 发布说明

契约 `0.7.0-p7`，SQLite schema v7。P6 实现提交 `0273055`，P7 实现提交 `e3a00f6`，最终能力/备份校正 `7607dfc`。P6 的30项及P7 SYS07已交付，全部105项 implemented、partial/planned 均为0。使用与逐项证据见 [P7指南](http-api-p7-guide.md)、[验收](http-api-p7-verification.md)、[操作报告](http-api-p7-operations.md)。

- 新增 admin 授权 `/metrics`，提供有界队列/执行、请求错误、任务收尾与确认传输字节。固定标签，没有账号/job/owner、路径、URL 或秘密；抓取不扫描历史或阻塞执行锁，计数器重启归零。
- P6 已支持摘要/修复/校验/导出/增量、本地 AES/gzip、完整 password/qrcode HTTP 会话、owner context、固定来源管理、诊断和浏览器续传接收。密钥及登录交换秘密不写公共结果，重启缺密钥明确中断。
- 修正本地摘要INT03的凭据声明，不再错误要求BDUSS；修正 test=true 下载写入字节统计：实际传输仍计数，`written_bytes=0`。rsync 比较大小、dry_run 不传输、路径/选项与旧请求幂等绑定保持。
- 加入停机完整备份/恢复工具及真实旧二进制演练，覆盖 WAL/SHM、接收 offset、ENOSPC/损坏/符号链接拒绝、v6升级与完整旧快照回滚；过期资源 tombstone 保留一天后有界清理。
- 同步 YAML/内嵌 JSON、能力真实状态、105组交换、25条行为规格、622条源码映射及 CI 的14组实际响应。CLI原有构建目标和独立配置边界保留。

升级前必须停止服务并保存主密钥、数据库及日志、身份、artifacts/staging/接收数据的完整快照，外部启动/TLS/token/legacy配置配套保存。旧二进制拒绝v7；回滚恢复升级前整套快照，不能降低数据库版本号。

INT06合法请求仍按长期不可用503处理；NoSplitFile/Rname/SuperTransfer非默认仍明确拒绝。实现完成不等于真实百度全部成功：P6 INT02严格errno解析后被上游拒绝，旧成功结论已撤回；P5同账号TRF02被拒绝。交互登录确认、账户级增量、离线/回收站与故障注入仅受控验证；13目标编译证据不等于其它目标机运行。没有自动push或部署。
