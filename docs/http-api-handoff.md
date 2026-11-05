# HTTP API 工程交接：P4/P5 已交付，后续为 P6/P7

日期：2026-11-05。分支 `codex/http-api-design`；P3 开发基线 `4fc00ce`。契约 `0.5.0-p5`，SQLite schema v6。原业务源码基线仍为 `1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73`，P0/P1 历史清单保留。

## 1 当前交付

| 范围 | 当前事实 | 验收口径 |
| --- | --- | --- |
| P0/P1 | 105 操作、89 路径、191 schema；12 个系统/配置操作保持 implemented | 当前源码映射 622 条；映射、实现、离线和上游实测分开统计 |
| P2 | 账号/单文件/任务、持久派发、取消、关闭和恢复保留 | [P2 验收](http-api-p2-verification.md)及原真实百度脱敏报告不重写 |
| P3 | 完整 ACC01—ACC09、FIL01—FIL06、JOB01—JOB06/JOB09/JOB11、ART01—ART04、LOC01 | [P3逐项验收](http-api-p3-verification.md)、[操作指南](http-api-p3-guide.md) |
| P4 传输 | UPL01—UPL04、DWN01—DWN04、JOB07—JOB08及独立DWN05扩展 | 完整目录、策略、并发、限速、真实暂停、暂存引用、内容代理和票据见P4记录 |
| P5 写业务 | FIL07—FIL11、SHR01—SHR04、TRF01—TRF02、OFF01—OFF07、REC01—REC04、JOB10 | [P5逐项验收](http-api-p5-verification.md)、[指南](http-api-p5-guide.md) |
| 上游 | 单个授权扫码Cookie账号、批准目录内各轮资源 | P3为33阶段，P4为39阶段；P5文件写入/新分享/检查通过，TRF02被百度明确拒绝，离线与回收站仅受控验收，详见各阶段报告 |

当前为 **74 implemented、0 partial、31 planned**。P3 的24个归属操作和4个P2账号切片已完整核销；P4十个核心操作及独立DWN05扩展、P5全部23项均已核销。planned 操作授权后返回501。QR辅助认证不计为LOG操作实现；HTTP登录会话仍在P6。

推荐先读：本文件 → [P5指南](http-api-p5-guide.md) → [P5验收](http-api-p5-verification.md) → [P4指南](http-api-p4-guide.md) → [P4验收](http-api-p4-verification.md) → [P3指南](http-api-p3-guide.md) → [P3验收](http-api-p3-verification.md) → [实施计划](http-api-implementation-plan.md) → [设计](http-api-design.md) → [源码覆盖](http-api-coverage.md) → [契约校验](../api/openapi/README.md)。

## 2 代码接入点

| 位置 | 当前职责 | 后续扩展边界 |
| --- | --- | --- |
| bootstrap/server.go、p3.go | 目录锁后装配唯一服务、固定旧账号来源、维护清理、ready和有序关闭；p3Engine按kind路由 | P4/P5已沿现有生命周期装配，不再重建任务基础 |
| application/accounts、persistence/accounts.go | 规范凭据、独立ID、版本/归档/吊销、强ETag、配额、删除和幂等 | 交互登录在P6；身份未知字段保持null，UK不冒充UID |
| infrastructure/legacyconfig | 显式legacy_accounts_file只读导入，不使用CLI全局对象 | 源指纹/索引永久重放，重复UID不合并；CLI默认值和客户端参数导入/管理在P6 |
| application/files、infrastructure/baidu/files.go | 列表/元数据、搜索、通配符、流式扫描；实例客户端和有界上游响应 | P5文件写入已复用读取；绝对/base_path先行，不依赖P6 context |
| baidupcs/file_directory.go、pcscommand读取路径 | 新单页方法共用解析；旧聚合排序/输出保留，重复页/中途失败明确返回错误 | 不复制旧版分页解析；P3 CLI回归可直接复用 |
| application/transfers、domain/transfer | 2执行/16等待、持久派发、取消、查询、逐项、retry与P2 resume | 不引入通用工作流。新kind按原始权限检查；未知提交先核验，不盲目retry |
| persistence/transfers.go、events.go、queries.go | 任务/事件/控制重放同事务、独立序号、快照、逐项索引和高水位游标 | 当前v6增量迁移，保留旧记录/版本/检查点；不能重建DB代替迁移 |
| httpapi/events.go | 一致snapshot→追踪、持久游标回放、心跳、有界慢消费者断开 | 每连接128帧、全局64连接；终态和序号同时可见，不漏最终事件 |
| application/artifacts、localfs/artifacts.go、persistence/artifacts.go | 管理结果文件、授权、Range/HEAD、TTL/配额、引用、清理与tombstone | 暂存/上传接收在P4/P6；结果文件与用户root/秘密目录隔离 |
| infrastructure/baidu/transfer.go、localfs/open_* | 已验证的单文件上传/下载、目标锁、指纹、提交核验和Unix目录描述符 | P4扩展已完成；不复用旧全局上传客户端或下载monitor/cache |
| requester/downloader/context.go | 独立连接、256KiB持久确认、严格Range、请求取消/空闲期限 | 旧Execute并行路径cache已按实例分配，P4隔离测试保持 |

内部 transfer.Record/Checkpoint 仍含代理、uploadid和私有临时文件信息，只能加密持久化，不能作为HTTP DTO或直接写日志。事件内部载荷同样加密，HTTP必须经公开投影。大小、ID、revision均为规范十进制字符串。

## 3 P3 已固定的语义

1. 保留同module、标准库HTTP、五层、单实例SQLite和目录独占锁；每个port有当前使用者，不增加外部队列/微服务。
2. 普通凭据更新归档旧版本，已有任务保持原绑定。null清除撤销旧版本并销毁历史秘密载荷，关闭对应任务及同步读取连接；删除有活动/待核验任务的账号返回409，成功删除时撤销全部版本。cookies=null保留独立token，bduss=null清除依赖Cookie会话；空字符串/混合Jar输入拒绝。
3. 凭据吊销不代表远端回滚。committing/done或未知提交仍interrupted，不假报failed/cancelled，也不允许retry盲目重放。P5的JOB10使用当前凭据只读核验；换版需与原计划百度UK相同，证据不足保持interrupted。
4. 搜索/任务列表快照最多10000项、600秒，绑定查询/调用方/账号或原始权限。扫描逐项使用不可变条目高水位，支持大于10000项；不将后续新增项混入旧游标。retry只新建符合scope的项，最多10000项，重新准备并使用当前配置/凭据；不复制uploadid/下载临时文件。
5. 扫描支持tree/recursive/statistics、depth、include_ids和节点/时间上限；截断complete=false。取消按未完成选择根保存重试单位，避免为遍历栈中的所有节点逐一执行取消。重启保留interrupted，扫描取消后retry，不宣传原位续读。
6. 任务查询在原权限过滤后计数并保留时间精度；历史P2提交重放queued回执不改写当前任务。状态、检查点、逐项、控制重放与事件在相应事务内提交；单job序号独立持久化，清理不复用。SSE初次一致snapshot后追踪，重连400/409/410语义固定，慢消费者断开但不阻塞任务。
7. Artifact写入完成同步后发布，结果完成后24小时TTL；256MiB单文件、1GiB总量、128文件；活动任务/响应引用保护物理文件。删除保留任务摘要，过期/删除读取410，HEAD任何状态无body。
8. token-only只开放已实现的token验证/配额；能力的credential_alternatives表达替代凭据组合。implemented不自动等于上游available，verified_at无证据时null。
9. P2 Request新增字段在空值时省略，保持4fc00ce的检查点绑定和幂等job_id算法；v3旧凭据首次变更前兼容读取、变更时归档。P3增加了专门兼容测试。

历史P2受限有效值见[P2指南](http-api-p2-guide.md)：普通文件1字节至32MiB；上传单文件/单分片并发、skip、norapid、retry=0；下载pcs/relative、并发1、校验开启、overwrite=false；两种速率字符串"0"。省略字段按配置继承，P4已扩展该子集，当前支持值以P4指南及capabilities为准。

## 4 阶段完整验收归属（P4/P5已核销）

| 阶段 | 操作数 | operationId |
| --- | --- | --- |
| P4 | 11 | DWN01、DWN02、DWN03、DWN04、DWN05、JOB07、JOB08、UPL01、UPL02、UPL03、UPL04 |
| P5 | 23 | FIL07、FIL08、FIL09、FIL10、FIL11、JOB10、OFF01、OFF02、OFF03、OFF04、OFF05、OFF06、OFF07、REC01、REC02、REC03、REC04、SHR01、SHR02、SHR03、SHR04、TRF01、TRF02 |
| P6 | 30 | CFG06、CFG08、CFG09、CTX01、CTX02、CTX03、CTX04、EXP01、FIL12、INT01、INT02、INT03、INT04、INT05、INT06、LOC02、LOC03、LOG01、LOG02、LOG03、LOG04、LOG05、LOG06、SYS05、SYS06、UPL05、UPL06、UPL07、UPL08、UPL09 |
| P7 | 1 | SYS07 |

| 阶段 | 不可遗漏的工作 |
| --- | --- |
| P4 | 目录/空文件/策略和全部有效选项；秒传/非秒传、最新握手/-9分支、uploadid失效；同名/相对层级、线程与文件预算、完整限速、按实例暂停取消恢复；暂存引用/磁盘不足/不覆盖提交；代理重定向/Range/HEAD/慢流和票据隔离。旧下载monitor/cache、上传全局停止仍须隔离，不能只增加ClientFactory；相应CLI行为同步回归 |
| P5 | 先修复baidupcs.cpmvOp丢失prepare错误并加回归；写入冲突与部分成功。分享任意有效period/提取码/combined_link、整段分享文本/fs_id/检查分页/collect、转存可见性和下载子任务。OFF03 >100项拒绝、OFF02不假报超过上游实际1000项的全量；回收站恢复实际ID、LeftTime不猜单位；未知结果reconcile不盲目重试 |
| P6 | 长度/MD5/slice/CRC32、MD5修复、导出root/格式/部分失败/结果文件和增量cursor/reset/不可用状态；保留原password/challenge流程，未知challenge拒绝；加解密默认不删源、密钥只写、缺失秘密明确不可恢复。context/CLI默认值/固定配置reload/诊断后置，不共享可写CLI配置，不恢复全局当前账号 |
| P7 | 汇总P2起的证据、完整核心覆盖、CLI及目标机运行、备份/迁移/回滚、故障注入/大目录/慢网络/磁盘不足/资源上限和发布；SYS07无秘密或无界label。映射率/实现率/离线/上游分别统计，CGO=0交叉编译不等于目标机运行 |

浏览器续传接收、DWN05票据、qrcode登录扩展、xpan/旧参考路由单列扩展，不阻塞核心。INT06、NoSplitFile、Rname、SuperTransfer必须有真实状态或明确不支持；历史UI选项不代表上游已经恢复。LOG原password/challenge和qrcode分开核销，不把本轮辅助扫码算入LOG实现。

## 5 存储、安全和生命周期

- 数据目录独立，master.key/DB/artifacts配对备份，密文不能换新key解读。当前v6由v5增量升级，保留v4→v5链路；旧二进制拒绝高版本schema，回滚需恢复升级前一致备份。P0/P1冻结清单不重写。
- bootstrap先获得目录锁再恢复/派发。直接组合Store/Service的单元测试不等于允许生产多实例。
- 单独SQL状态/时间/version为权威，所有提交检查错误。维护每分钟处理事件/幂等/无引用到期结果；活动job重放保留，旧配置导入指纹长期保留。未承诺主机掉电一致性。
- credentials、proxy、uploadid、签名URL/票据、本地绝对root不进DTO/日志。日志仅路由模板；401仍是服务API身份问题，百度失效为422。
- 本地root按真实目录与软链接检查；Unix打开/提交复用目录描述符，不覆盖策略和临时文件归属必须在并发、取消和重启后保持。
- HTTP排空最多10秒，任务检查点/终态等待15秒，必要时取消连接后再等5秒；存储不抢先关闭。SSE观察draining退出；流开始后错误只终止流，不追加JSON。

## 6 验证与继续工作

完整复现命令、测试名、实际响应及上游口径见[P3验收](http-api-p3-verification.md)。CI为`.github/workflows/http-api.yml`。相关包/race、旧CLI离线回归、105操作契约和13目标构建均应保持；已知原基线下载器测试/绝对路径/外网示例仍按[P0基线](http-api-p0-baseline.md)单列，不能以go test ./...替代明确验收范围。

契约变更：YAML/设计/示例 → export_contract.py → 实际HTTP响应重新导出 → checker。源码或覆盖变化先人工审矩阵，再--write-inventory，最后不带该参数检查。当前622项包含P4执行原语、P5 CopyMovePolicy与ParseShareInput；新分层内部协作者按覆盖矩阵分组管理，不按函数名制造HTTP端点。

P4/P5开发已完成；从P6剩余业务与管理继续，沿既有执行器、持久队列和查询/事件/结果能力扩展。用户已授权在本工作区0600的`.env`中保留新扫码凭据供后续验证复用，文件已保存、由Git忽略且未被跟踪；复用命令见[P3验收](http-api-p3-verification.md)。临时二维码及测试服务数据已清理，远端本轮子目录清理通过。远端文件操作授权仍仅限`/BaiduPCS-Go-P2-Verification/`；凭据缺失或过期时再联系用户扫码，不读取日常账号或文件。所有实测记录时间、平台、参数及范围，交接者无需依赖聊天或本机/tmp判断剩余工作。

## P4 收尾与下一步

P4十个核心操作和DWN05独立扩展已实现，详细限制、证据和迁移回滚见[P4指南](http-api-p4-guide.md)及[验收](http-api-p4-verification.md)。SingleFileEngine保留P2检查点和握手/提交边界，新增transfer_plan冻结目录与源指纹；暂存引用与任务同事务。JOB07真实排空后paused，恢复给新context。CLI TaskUnit已显式注入客户端/限速/调用控制，旧下载cache按实例分配。schema v5只增加staging_files/staging_references。真实百度本轮39阶段和清理通过，参数范围见[P4报告](http-api-p4-upstream-verification.json)。P5已修复cpmvOp准备错误并回归；不得重置P3/P4/P5本地提交。

## P5 收尾与下一步

P4交付提交为f5976ad；P5首先以1cf0e91修复核心prepare错误和十进制app_id，完整实现提交为79ec471。P5写业务沿旧调度器增加14种写任务，应用层writes负责计划/执行/只读核验，Share/Offline/Recycle/File端口绑定独立客户端。v6仅新增inspection和同步分享提交意图表。普通凭据换版、null撤销、旧检查点绑定、retry新建、持久SSE与artifact语义保持。

P5逐项状态及复现见[P5验收](http-api-p5-verification.md)，使用及v6一致备份/回滚见[P5指南](http-api-p5-guide.md)。JOB10不能证明结果时仍保留interrupted，不能盲目重发；取消不能回滚百度事务。SHR01同步未知回执保留待核对意图。OFF02仅当前1000项窗口，OFF03超过100拒绝；回收站LeftTime不猜单位，恢复记录实际ID。

真实P5仅触及本轮目录和新创建分享，二者清理通过；未调用全局离线或回收站清空。TRF01真实通过，TRF02同账号转存被上游明确拒绝，成功转存/collect/下载子任务为受控证据。NoSplitFile、Rname和SuperTransfer保持明确不支持。后续P6的30项和P7的SYS07共31项仍planned；主检出独立QR工作保持隔离。

实际开发目录为`/workspace/github/BaiduPCS-Go-worktrees/codex/http-api-design`，分支`codex/http-api-design`；主检出`/workspace/github/BaiduPCS-Go`中的独立QR工作保持隔离。P3的d1ae687/cd75eb4及后续P4/P5提交均保留在本地提交链中。验收代码、报告和复现入口已提交；`.env`保留0600、Git忽略且未跟踪。后续任务从本worktree的P6/P7范围接续。
