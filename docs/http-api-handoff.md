# HTTP API 开发交接：从 P2 继续

日期：2026-11-02。分支：`codex/http-api-design`，远程：`fork`（`zerovoxxx/BaiduPCS-Go`）。以本文件所在提交为交接版本；`1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73` 是原业务源码基线，`bf76d65` 只有早期设计，不能作为 P1/P2 已实现版本。

## 1 先确定当前完成到哪里

| 范围 | 当前事实 | 不能据此推断 |
| --- | --- | --- |
| P0 | 105 个操作、89 个路径、190 个 schema；当前 615 条源码映射；公共协议、示例及旧 CLI 基线已有校验 | 105 个操作可用，或 25 组行为规格已经执行 |
| P1 | server、配置快照、隔离客户端、Bearer、SQLite、生命周期及 12 个系统/配置操作可调用 | 账号、任务和传输已经接入 server |
| P2 | 内部账号导入、单文件执行、取消、检查点、组件重开及结果核对有受控 TLS/race 证据 | 完整 HTTP 端到端、服务进程 SIGKILL 恢复、真实百度传输已通过 |
| QR 独立试验 | [QR 设计](qr-login-design.md)与[脱敏记录](qr-login-verification-2026-11-02.json)证明当时 macOS 环境下的扫码及只读会话链路 | qrlogin 命令、HTTP 登录会话、Linux 运行或上传下载已实现 |

当前只有 SYS01—SYS04、SYS08—SYS09、CFG01—CFG05、CFG07 为 implemented，其他 93 个操作仍 planned，授权后返回 501。P2 整体未验收，下一位工程师应先完成第 4 节，不能直接跳到 P3。

推荐阅读顺序：本文件 → [实施计划](http-api-implementation-plan.md) → [P2 证据与限制](http-api-p2-verification.md) → [设计](http-api-design.md) → [OpenAPI 与校验](../api/openapi/README.md)。逐项业务语义查[源码覆盖矩阵](http-api-coverage.md)，部署查[启动指南](http-api-server.md)。

## 2 代码接入点与缺口

| 位置 | 已有能力 | 接手时要做的事 |
| --- | --- | --- |
| [bootstrap/server.go](../internal/bootstrap/server.go) | 数据目录锁、密钥/存储、ClientFactory、P1 服务、HTTP 与关闭 | 当前没有创建 accounts/transfers 服务；每个独占数据目录只装配一个任务执行服务，增加启动恢复、readiness 和关闭顺序 |
| [httpapi/server.go](../internal/httpapi/server.go)、dto.go | 12 个 handler、鉴权、严格 JSON、错误、版本比较 | 加入账号和任务依赖、DTO/handler；现有统一 query 拒绝规则不能机械沿用到后续带查询参数的接口 |
| [application/accounts/service.go](../internal/application/accounts/service.go) | 三种凭据规范化、独立账号 ID、加密导入 | 还没有账号列表/读取应用服务、在线校验、元数据/版本更新或 HTTP Account DTO；创建账号当前要求 admin 且账号 allowlist 有 `*` |
| [application/transfers/service.go](../internal/application/transfers/service.go) | 2 个运行名额、16 个等待名额；幂等提交、Get/Cancel/Resume/Close | 当前是内部执行切片，没有持久派发扫描、公共 Job/Result、控制请求幂等、同目标锁或完整错误映射 |
| [ports/transfers.go](../internal/ports/transfers.go) | 账号仓库、传输记录与执行边界 | 按当前调用者增补列表、事务、结果和版本能力；不先建一个包含所有百度方法的大接口 |
| [persistence/sqlite.go](../internal/infrastructure/persistence/sqlite.go)、transfers.go | schema v2；配置/账号/记录/检查点加密，状态 CAS | 增量迁移、任务事务与索引、更新时间、幂等保留、结果及清理；不能重建数据库代替迁移 |
| [baidu/factory.go](../internal/infrastructure/baidu/factory.go)、transfer.go | 实例级网络和凭据绑定；真实单文件适配 | 现有切片只支持 1 字节—32 MiB、skip、上传并行 1、单连接下载、无速率限制；完整选项仍需实现 |
| [downloader/context.go](../requester/downloader/context.go) | 独立单连接 context/Range 路径 | 不是旧 Execute/monitor 已完成并发隔离的证明；P4 再接原并行能力 |
| [baidu/transfer_test.go](../internal/infrastructure/baidu/transfer_test.go) | 可运行的装配与受控上游示例 | 可复用 fixture 思路，但不能把测试装配、假上游或原始内部记录变成公网 API |

`transfer.Record` 含完整配置和私有检查点，可能包括代理秘密和 uploadid；`account.Entry` 的 JSON 也不是契约 Account。必须映射 DTO，不能直接 `json.Encode` 应用返回值。SQL 的 `jobs.state` 是当前权威状态，加密 record 中同名字段可能滞后。

## 3 已确定的接续决策

1. **五层与 P1 保留。** 同 module、模块化单体、标准库 HTTP、单实例 SQLite；不引入外部队列或通用工作流。管理能力不作为核心传输前置，CLI 随对应流程逐步接共享应用服务。
2. **P2 对外开放前补齐 partial 状态。** 当前代码只有 planned/implemented。下一次 P2 契约演进采用 `planned | partial | implemented`：partial 表示有实际 handler，但只支持明确子集；implemented 表示完整目标契约验收。`x-phase` 仍表示完整验收阶段，`x-early-validation=P2` 不代表已开放。当前交接不提前修改运行中的状态声明。
3. **partial 必须贯穿同一变更。** 同步 YAML 的 Capability schema、`x-capability`、示例、`contract.Catalog`、`domain/system`、HTTP 路由开关、System.Capabilities 与 check_contract.py。partial 能执行；planned 仍 501；已知但不支持的选项 422，格式错误 400。实际 HTTP 校验必须覆盖 partial 和 implemented 的成功/拒绝路径，实现率分开统计。百度业务没有上游证据时保持 unverified、verified_at=null，不能照搬当前 P1“本地能力已验证”的 available 理由。更新契约版本，保持既有 P1 行为。
4. **先支持明确的受限传输。** P2 只需显式账号、单个本地源/绝对网盘文件路径、已有授权根目录、固定单文件选项；轮询即可。先不依赖 context 资源、staging、SSE、全部列表/搜索或交互登录。完整单文件/目录与选项在 P4 核销。
5. **秘密请求使用验证过的 HTTPS。** 不复用旧 `pcsconfig.ActiveUser/CopyPCS`、全局 uploadClient 或未经检查的身份查询回退。现有配置允许 `enable_https=false`，这是尚待 P2 适配的兼容边界；对携带凭据的明文上游请求须明确拒绝或使用明确的安全策略，不能静默泄露凭据，也不能声称该约束当前已经落实。
6. **身份、Cookie 与凭据版本分开。** UID、UK、服务 account_id 各有语义，未知保持 null；不以 UID 去重或用 UK 冒充 UID。当前 Cookie map 只是旧字符串输入的最小模型，不能容纳 QR 试验中的同名不同域 Cookie。扩展到结构化会话前保留 domain/path/Secure/expires；不得合并后把 Passport 的 STOKEN 广播到所有百度子域。检查旧 HTTPS/HTTP Cookie 查询和 locate 的 UID 回退，避免绕过实例客户端。QR 试验不是当前工厂已经支持结构化 Cookie 的证据。
7. **恢复先核验，取消不等于回滚。** known cancelled 不自动复活；提交阶段结果未知保持 interrupted。若 interrupted 任务处于 committing/done，不能直接“取消成功”并丢弃核对能力；先核验已发生的效果。新配置不改变已入库任务快照；凭据更新/吊销需要版本记录与明确恢复规则。

## 4 P2 剩余待办：按这个顺序提交

以下均未完成，除非另有实际测试证据。每项都应在实现后把测试名/证据链接补回，不以存在类型或函数核销。

| ID | 待办与修改点 | 完成条件 |
| --- | --- | --- |
| P2-01 | 落实第 3 节的 partial 契约/能力/路由状态；清楚表达文件数、字节上限、固定模式和选项值限制 | 文档与运行状态一致，partial 不误计完整实现，P1 操作无回归；不凭 supported_options 的字段名暗示所有值均可用 |
| P2-02 | ACC01/02/03/07：补账号仓库列表、脱敏读取、HTTPS 在线验证、身份及状态持久化、请求幂等；为缺失字段构造真实 nullable DTO | 三种凭据输入受控；导入不等于已验证登录；列表按 allowlist 过滤，秘密不回显；token-only 只声明已验证能力；force_login_username 等未支持输入明确拒绝 |
| P2-03 | bootstrap 装配账号与唯一传输服务；服务 context 独立于请求；注册 HTTP 依赖，恢复完成后才 ready | 无账号仍能启动；重复实例被目录锁拒绝；startup 失败不留下运行 goroutine；请求断开不取消已入库任务 |
| P2-04 | UPL01/DWN01 受限 DTO 映射与提交前验证：源/目标、权限、presence、路径、选项快照及上限 | 从契约请求映射到内部单文件 Request；`target_dir + 源文件名`、`destination + 远端 basename` 不混淆；显式 false/0 不丢失；不支持的目录、staged、dry_run 或选项不被忽略 |
| P2-05 | 持久化派发与幂等：修补“创建记录→内存 schedule”的窗口；区分提交/控制幂等作用域、请求摘要与保留期 | 入库后提交连接丢失或派发前失败，任务可查且不会永久滞留；并发同 key 只产生一次逻辑提交，不同 body 409；至少保留 24 小时、活动任务不提前清理；P3 再补任务/事件同事务 |
| P2-06 | JOB02/04/06/08 的受限 HTTP；构造 Job、Result、Progress、Problem、有效选项和 available_actions | 所有必填字段有真实含义；无总量用 null，计数/版本/字节为字符串；未完成 result 返回 409；权限含 owner、账号、root 与原操作 scope；重复控制遵循幂等规则 |
| P2-07 | 状态竞争、同目标互斥与排空：补可取消路径锁；检查 interrupted+committing 的 Cancel；把队列满和上游错误映射到契约 | 取消 A 不影响 B/C；同账号同远端目标、同实际本地目标不并发写，root 别名也不能绕过；已确认完成可胜过取消；未知提交结果不伪报 cancelled；队列满用 429/Retry-After，不直接暴露内部当前的 503 QUEUE_FULL |
| P2-08 | 接入 runtime 关闭：停止接收/提交，取消或排空任务，保存检查点并等待执行器，再关闭客户端、DB、目录锁 | 不沿用 P1“HTTP 排空后直接关 DB”的顺序；Close/终态/检查点写入有受控期限并报告错误，存储不能先于最后写入关闭；恢复/关闭过程中新请求明确拒绝 |
| P2-09 | 持久化与文件故障窗口：检查 SaveCheckpoint/Finish 的 RowsAffected、更新时间、迁移及唯一执行所有者；处理临时文件先建成但检查点未写、link 成功但终态未写等情况 | v1→v2→下一版升级不丢原配置和记录；无故修改密文/主密钥失败明确；遗留 running/cancelling 不被盲目重放；只清理确认属于该 job 的文件；目标存在与摘要不符时拒绝覆盖 |
| P2-10 | 真正的 HTTP 与进程级测试：接 Bearer→账号→提交→查询→取消/恢复→重启；覆盖 Range 错误与慢流 | httptest 实际响应通过 schema；上游忽略 Range、返回错误 Content-Range/长度、提前断流不能追加坏文件；取消请求和 SIGTERM 有界；用子进程 SIGKILL 验证“已入库/分片后/合并后/终态前”恢复，不以当前组件重开代替 |
| P2-11 | 专用账号真实上游验证与平台核对 | 显式提供测试凭据和独立网盘/本地目录，小文件上传回读、取消、重启核验；不使用正常文件，不保存凭据或目录内容；记录账号能力、应用参数、平台和时间。无凭据时可继续独立离线开发，但保持上游 unverified，不能用 QR 只读结果代替 |
| P2-12 | 补 P2 操作指南、HTTP 示例、能力限制、验收报告与覆盖状态，重新评估 P3/P4 余量 | 新接手者可从干净 clone 复现；完整矩阵只在影响它的变更后重跑；第 5 节后续任务未被最小切片冒充完成 |

P2-04 的最小测试请求应显式选择当前能执行的值：上传 `file_concurrency=1`、`parallel_per_file=1`、`max_retries=0`、`policy=skip`、`skip_rapid_check=true`；下载 `mode=pcs`、`path_mode=relative`、`parallel=1`、`file_concurrency=1`、`max_retries=0`、`overwrite=false`、`verify_checksum=true`，速率为 0。未列字段仍须按契约解析；继承配置后若超出最小执行能力，明确拒绝。当前内部引擎两种任务都会检查两个速率字段，错误多归为 INTERNAL_ERROR，skip 命中也未形成 skipped 分项结果，均不能原样当作完整公网行为。

P2 完成门槛：P2-01—P2-10、P2-12 的代码与离线/进程证据齐备；P2-11 有明确实测结果或未测/不可用结论。后者未取得时不能标记对应上游业务已实测可用；需要上线使用的核心路径仍必须补专用账号验证。已完成内部切片不是上述待办已经完成的证据。

## 5 P3—P7：完整验收归属与不可遗漏项

以下按当前 OpenAPI 的 `x-phase` 核对，共 105 个操作；P2 提前切片不改写完整验收归属。

| 阶段 | 操作数 | operationId |
| --- | --- | --- |
| P1 | 12 | CFG01、CFG02、CFG03、CFG04、CFG05、CFG07、SYS01、SYS02、SYS03、SYS04、SYS08、SYS09 |
| P2 | 4 | ACC01、ACC02、ACC03、ACC07 |
| P3 | 24 | ACC04、ACC05、ACC06、ACC08、ACC09、ART01、ART02、ART03、ART04、FIL01、FIL02、FIL03、FIL04、FIL05、FIL06、JOB01、JOB02、JOB03、JOB04、JOB05、JOB06、JOB09、JOB11、LOC01 |
| P4 | 11 | DWN01、DWN02、DWN03、DWN04、DWN05、JOB07、JOB08、UPL01、UPL02、UPL03、UPL04 |
| P5 | 23 | FIL07、FIL08、FIL09、FIL10、FIL11、JOB10、OFF01、OFF02、OFF03、OFF04、OFF05、OFF06、OFF07、REC01、REC02、REC03、REC04、SHR01、SHR02、SHR03、SHR04、TRF01、TRF02 |
| P6 | 30 | CFG06、CFG08、CFG09、CTX01、CTX02、CTX03、CTX04、EXP01、FIL12、INT01、INT02、INT03、INT04、INT05、INT06、LOC02、LOC03、LOG01、LOG02、LOG03、LOG04、LOG05、LOG06、SYS05、SYS06、UPL05、UPL06、UPL07、UPL08、UPL09 |
| P7 | 1 | SYS07 |

| 阶段 | 必做的具体工作与边界 |
| --- | --- |
| P3 | 完整账号与凭据版本/吊销、旧配置导入；只写字段更新需要 presence/null 语义，不能套用 P1 全局拒绝 null 的配置 decoder。补分页与搜索快照、批量逐项结果、文件扫描、任务/事件/幂等同事务、持久派发、retry 新任务、SSE 的 snapshot→追踪/重连/过期/慢消费者、artifact 的 Range/TTL/引用和授权。每次重开任务状态与版本仍正确；只迁移触及的 CLI 读取流程 |
| P4 | 完整目录/空文件/策略与有效选项，秒传/非秒传、最新握手/-9 分支、上传id失效、同名下载与相对层级、文件/线程预算、完整限速、按实例暂停/取消/恢复、暂存引用与磁盘不足、代理/重定向/Range/HEAD/慢流及票据隔离。旧下载 monitor/全局 cache 和上传器全局停止仍需隔离，不能只增加 ClientFactory。按账号+目标+源指纹管理断点；相应 CLI 行为同步回归 |
| P5 | 文件复制/移动/删除的冲突与部分成功；先修复 baidupcs.cpmvOp 丢失 prepare 错误的基线问题并加回归。分享 period/提取码/combined_link、整段分享文本与 fs_id 选择、检查分页、collect、转存后有界可见性与下载子任务。OFF03 >100 项拒绝，OFF02 不假报超过上游实际 1000 项的全量；回收站恢复保留实际成功 ID，LeftTime 单位不猜测；未知结果 reconcile 不盲目重试 |
| P6 核心及管理 | 校验长度/MD5/slice MD5/CRC32、MD5修复、导出 root/格式/部分失败/结果文件、增量 cursor/reset/不可用结论。保留原有密码 challenge 的有效流程，未知 challenge 明确拒绝。加解密默认不删源文件，密钥只写、不可恢复情况明确。context、CLI 默认值、固定来源配置 reload、诊断后置；不共享可写 CLI 配置，不重新引入全局当前账号 |
| P7 | 汇总从 P2 起的证据、全部核心覆盖、原 CLI 与目标平台运行、备份/迁移/回滚范围、故障注入、大目录/慢网络/磁盘不足/资源上限及发布文档。SYS07 指标不暴露账号/路径/令牌或形成无界 label。源码映射、完整实现、partial、离线验收及上游实测分别统计；CGO=0 交叉编译通过不等于目标机运行通过 |

扩展不阻塞核心业务版本：DWN05 短期下载票据、UPL05—UPL09 浏览器可续传接收、LOG03 及其他 LOG 操作的 qrcode 方法、xpan 与参考项目旧路由。LOG01/02/04/05/06 的原有 password/challenge 业务与 qrcode 扩展分开核销。INT06、NoSplitFile、Rname、SuperTransfer 等须有真实状态或明确不支持，不能把历史不可用能力写成成功；UI/CLI 选项映射不代表第三方上游已经恢复。

## 6 存储、版本和安全边界

- 服务数据目录独立，`master.key` 与数据库必须配对备份；不得为已有密文生成新 key。迁移保持旧记录可读，若修改内部 Record/Checkpoint 的编码或 Cookie 模型，增加版本/转换与回滚限制说明。已有 `baselines/p0-source-inventory.json` 与 `p1-source-inventory.json` 是历史快照，不随实现重写。
- 当前目录锁在 bootstrap；transfers.New 会把遗留活动记录中断，所以只能由获得锁的唯一实例调用。测试直接组合 Store/Service 不等于允许生产多实例。
- 存储写入、检查点与终态必须检查实际提交结果；超时/失败不返回已持久化的假成功。保留周期、清理配额、备份一致性与主机掉电语义需明确；本轮证据未承诺主机掉电一致性。
- account/credentials、proxy、uploadid、签名 URL、票据和本地绝对根目录不进入公共 DTO、日志或事件；按路由模板记录。服务 API 的 401 与百度凭据失效分开，401 不能被当作注销百度账号。
- 本地文件按真实根目录和父目录/符号链接检查；最终文件不覆盖策略和临时文件归属在并发、取消及恢复时仍成立。需要处理文件打开/提交竞争，不能仅相信字符串前缀或 basename。

## 7 干净环境复现与 CI

在仓库根目录运行，Go 最低 1.23.0，验证工具链固定 1.23.12；Python 3.10+。首次安装依赖需要网络，受控测试不访问真实百度。

```sh
export GOTOOLCHAIN=go1.23.12
python3 -m venv /tmp/baidupcs-http-api-venv
/tmp/baidupcs-http-api-venv/bin/python -m pip install -r api/openapi/tools/requirements.txt

BAIDUPCS_P1_CONTRACT_RESPONSES=/tmp/baidupcs-http-responses.json \
go test -race -count=1 -timeout=120s \
  ./internal/domain/... ./internal/application/... \
  ./internal/infrastructure/... ./internal/httpapi ./internal/bootstrap .

/tmp/baidupcs-http-api-venv/bin/python api/openapi/tools/check_contract.py \
  --live-responses /tmp/baidupcs-http-responses.json

go test -count=1 -timeout=120s \
  ./pcsliner/args ./pcsutil/cachepool ./pcsutil/converter \
  ./pcsutil/escaper ./pcsutil/waitgroup ./pcsutil/taskframework \
  ./baidupcs/netdisksign ./baidupcs/expires/cachemap \
  ./requester ./requester/rio ./requester/rio/speeds \
  ./requester/uploader ./internal/pcscommand ./internal/pcsfunctions/pcsupload

python3 api/openapi/tools/check_builds.py --report /tmp/baidupcs-http-builds.json
CGO_ENABLED=0 go build -o /tmp/baidupcs-http .
/tmp/baidupcs-http server --help
git diff --check
```

上次交接前的有效结果：新增相关包/race 与 14 个既有离线包通过；18 组实际 HTTP 响应覆盖 12 个 P1 操作；105 个目标操作、190 个 schema、67 个边界例、615 条映射通过；13 个 CLI 目标 CGO=0 构建通过，SQLite 仅在 5 个初始 server 目标导入。未来新增 partial/handler 后增加实际响应覆盖，不能继续只导出原 18 组。

默认不要直接把 `go test ./...` 当作验收命令。已知原基线问题是 downloader 测试包 `ShowWorkers` 编译失败、pcsutil.TestWalkDir 和 checksum.TestChecksum 的开发者绝对路径；getip 和旧下载示例会访问外网/写文件。详情见[P0基线](http-api-p0-baseline.md)。P4 改动旧下载器时先修正其测试入口与 fixture，不能因为包当前不可测试而跳过新逻辑验证。

契约变更流程：修改 YAML/设计/示例 → `python3 api/openapi/tools/export_contract.py` → 实际 HTTP 测试重新导出响应 → checker。源码或覆盖行变化时，先审人工矩阵，再运行 checker 的 `--write-inventory`，最后不带该参数检查；不能用重建清单掩盖未分类项。现有 CI 在 `.github/workflows/http-api.yml`，根 README/发布说明须在对外功能变化时更新。

## 8 交接验收方式

第一个后续变更应引用 P2-01 起的具体 ID，列出支持的请求、仍不支持的字段和运行证据；代码落点以上述接入点为准。每次只把有证据的项改为完成。P3—P7 按操作表和覆盖矩阵逐项核销，发现上游不可用则记录限制与证据，不补造成功响应。所有真实账号/协议实测均记录时间与范围；任何后续工程师不需要依赖这段聊天或本机 `/tmp` 文件才能判断还有什么没做。
