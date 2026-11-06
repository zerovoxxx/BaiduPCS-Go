# HTTP API 工程交接：P6/P7 已交付

日期：2026-11-05。业务分支 `codex/http-api-design`，开发目录 `/workspace/github/BaiduPCS-Go-worktrees/codex/http-api-design`。契约 `0.7.0-p7`，SQLite schema v7。原业务基线 `1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73` 及本地 P2—P5 提交全部保留；P6 实现提交 `0273055`，P7 实现提交 `e3a00f6`，最终能力/备份校正 `7607dfc`。

## 1 当前交付

**105 implemented、0 partial、0 planned；89 路径、191 schema、622 条 AST 源码映射。** 本次接续的 P6 全部 30 项和 P7 SYS07 均完成。浏览器续传接收及完整 HTTP password/qrcode 会话已独立服务化，不依赖主检出的辅助 QR 工作。

| 资料 | 内容 |
| --- | --- |
| [P7 逐操作报告](http-api-p7-operations.md)、[机器可读报告](http-api-p7-operations.json) | 全部 105 项的实现、受控响应、源码映射、真实上游状态与边界 |
| [P7 验收](http-api-p7-verification.md)、[验收 JSON](http-api-p7-verification.json) | 完整契约/race/CLI、故障/恢复和平台证据 |
| [P7 指南](http-api-p7-guide.md) | 授权指标、可执行停机备份、迁移、恢复、回滚 |
| [P6 指南](http-api-p6-guide.md)、[P6 验收](http-api-p6-verification.md) | 摘要/校验/导出/增量、本地工具、登录、上下文、管理及浏览器接收 |
| [P5 验收](http-api-p5-verification.md)、[P4 验收](http-api-p4-verification.md)、[P3 验收](http-api-p3-verification.md)、[P2 验收](http-api-p2-verification.md) | 保留各阶段历史参数与账号范围，不扩大实测结论 |
| [原 P6/P7 接续指令](http-api-p6-p7-handoff.md) | 收到的授权与开发基线，作为历史输入保留 |

实现率、源码映射率、受控验收和真实百度状态分开报告。INT06 的合法请求返回明确的长期不可用 503、retryable=false；该已实现语义不属于 planned。密码/短信/扫码确认、账户级增量、离线和回收站等仅受控验证。P6 INT02 被百度明确拒绝，历史忽略 errno 的成功结论已撤回；P5 同账号 TRF02 同样保留拒绝结果。没有为测试要求用户再次扫码。

## 2 代码落点

| 位置 | 当前职责 |
| --- | --- |
| bootstrap/server.go、p3.go、p4.go、p5.go、p6.go | 单实例锁后装配、生命周期、能力与固定来源；p3Engine 路由现有任务执行器 |
| application/accounts、infrastructure/baidu/factory.go、persistence/accounts.go | 独立账号、凭据版本/归档/吊销、实例客户端和 UK 身份核验 |
| application/transfers、persistence/transfers/events/queries | 2 执行、16 等待；持久任务、逐项、事件、幂等、控制回执、未知结果只读核验 |
| application/tools、domain/tool、infrastructure/baidu/tools.go、localfs/tools.go | 摘要/修复/校验/导出任务、本地受限加解密、进程临时密钥及重启清理 |
| application/login、infrastructure/baidu/login.go | owner 隔离的 password/qrcode/challenge/complete/TTL，会话内保存并验证交换态 |
| application/contexts、httpapi/p6_contexts.go、persistence/resources.go | owner/account/cwd/强 ETag；入口统一冻结路径与原始幂等输入 |
| infrastructure/legacyconfig、application/configuration | 固定来源 reload、独立 CLI 默认值快照；不使用可写 pcsconfig 全局对象 |
| localfs/receptions.go、staging.go、persistence/staging.go | 受限接收、持久 offset、独立向百度传输检查点、完成发布和引用保护 |
| domain/metrics、application/transfers/metrics.go、httpapi/server.go | 固定标签内存计数器和有界派发快照，抓取不扫描历史或取得执行锁 |
| api/openapi/tools/backup_service.py、check_recovery.py | 停机一致备份/恢复、真实旧二进制拒绝高版本与完整快照回滚演练 |

## 3 P3 已固定的语义

本节锚点保留供历史文档引用，规则继续生效：

- 同 module、标准库 HTTP、五层依赖、单实例 SQLite 和目录独占锁，不重建通用任务平台。
- HTTP 不借用 pcsconfig.Config、全局当前账号、全局停止/信号；账号、路径、凭据版本与选项在提交时冻结。
- 普通凭据更新保留旧绑定版本；null 清除撤销。未知提交仍 interrupted，不假报 failed/cancelled，不回滚远端、不自动重发。JOB10 只读核验，换凭据需原计划与当前有效 UK 一致，UK=0 不授权。
- retry 创建新任务、重新准备，不能复制旧 uploadid、临时文件和检查点。P2 新增空字段序列化及幂等 job_id 兼容保留。
- 持久事件序号与终态同事务，SSE 重连、高水位游标、artifact TTL/引用/权限及 paused/interrupted 的 staging 保护保留。
- 分享选择、写入逐项部分成功、同步 SHR01 意图和未知结果禁止重放保留。核验后的下载子任务只包含确认成功的转存项。

## 4 P6/P7 新增边界

- link_ref 只接受本服务签发的 owner/account/凭据版本/TTL 引用；目标逐跳校验，不接受任意 URL/代理。
- 本地 AES 流格式保持历史兼容，gzip 默认开启、源默认保留；提交完成且明确 remove_source=true 后才删源。密钥仅在进程内存，备份不恢复密钥，重启缺失时明确不可恢复。原始无认证 AES 错误密钥无法保证检测，不宣传其具有认证完整性。
- 登录仅通过已保存且核验的交换状态建立/更新账号；不接收临时 BDUSS 绕过 LOG05。owner、原权限、方法、TTL、挑战和并发状态相互隔离，complete 幂等。
- context 与 base_path 互斥，跨账号冲突明确；后续 context 更新不影响已有任务或同 Key 重放。CLI 默认值独立于 HTTP context 和运行任务。
- reload 请求不能选择文件，不能扩大身份/root/网络权限。SYS05 主动探测固定目标与超时；SYS06 不猜有效期、不返回签名。
- 浏览器 PATCH 偏移在 fsync 与 DB 提交后前移；恢复截断未提交尾部。未完成不发布 staged_file_id，重复 complete 返回同 ID；并发按会话串行，延续暂存总量/数量/TTL/owner/引用限制。
- 指标只有固定标签，无 ID、路径、URL 或秘密；计数器重启归零，记录本进程已确认结果。test=true 下载实际传输但 written_bytes=0，P7 已补充结果与指标回归。
- v1—v6 增量迁移到 v7；备份包括主密钥、DB/日志、artifact、staging/接收及身份文件；回滚恢复旧完整快照，禁止降低 schema 号冒充兼容。

## 5 继续维护

当前授权开发任务已全部交付，无剩余 planned/partial 操作。CI `.github/workflows/http-api.yml` 导出 14 组实际 HTTP 响应、校验全部 105 项、25 条行为规格和 622 条源码映射，运行相关 race、14 组 CLI 离线回归、恢复演练及 13 目标构建。

13 目标编译结论与实际 Linux/amd64 运行结论分开。P0 downloader 测试编译失败、固定开发者路径和外网示例仍按基线单列，不以无筛选 go test ./... 替代验收。

不自动 push/部署。不修改主检出的独立 QR 工作。真实验证继续只读使用被忽略且未跟踪的 0600 `.env`，仅操作 `/BaiduPCS-Go-P2-Verification/` 的当轮子目录并清理；不输出或提交 Cookie、密码、Jar、密钥或批准目录变量值。
