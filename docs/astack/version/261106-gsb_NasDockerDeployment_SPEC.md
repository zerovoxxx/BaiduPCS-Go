# NAS Docker 部署与现有 HTTP 服务适配

> **文档信息**
>
> | 字段 | 值 |
> |---|---|
> | 文档类型 | SPEC |
> | 需求 ID | 261106-gsb |
> | 文档状态 | 待实施 |
> | 发布版本 | 未定 |
> | 创建日期 | 2026-11-06 |
> | 最后更新 | 2026-11-06 |
> | 作者 | zerovoxxx |
> | 关联文档 | 本次 grill-me 讨论 Q1—Q18 及主干核查；[HTTP P7 验收](../../http-api-p7-verification.md)；[HTTP 契约](../../../api/openapi/README.md) |
> | 一句话目标 | 复用现有 HTTP 服务，在当前群晖 NAS 上以独立 Docker 容器提供受 Token 保护的下载 API，支持默认 3 个任务并发、服务总限速及符合恢复条件的下载自动续传。 |

## 目标与非目标

### 目标

1. 交付镜像构建文件、Compose、启动配置示例及 NAS 操作文档；实施阶段在当前 NAS 完成独立容器部署与验证。
2. 使用本项目已有 `server` 和 `/api/v1/`，通过 Docker 共享网络向同 NAS 的其他容器提供 HTTP API，使用 Bearer Token，不发布宿主机端口。
3. 服务一个个人百度账号。首次登录、凭据失效后的重新登录由用户在容器终端发起扫码；会话持久化，API 可查询账号和凭据状态。
4. 接收自己的网盘路径或分享链接及提取码。分享通过已有转存接口自动转存到专用网盘目录，再创建下载子任务；网盘副本由用户手动清理。
5. 下载根目录可配置，可指向 MoviePilot 监听目录。每个任务使用独立子目录，保留原文件名及源目录结构；临时文件与完成文件位于同一目标目录，完成后发布原文件名，供 MoviePilot 后续识别、整理。
6. 复用已有异步任务、进度、取消、重试、幂等和 SQLite 持久化。取消后停止写入并删除该下载任务的未完成文件，保留完成文件和已经产生的网盘副本。
7. NAS 部署默认同时执行 3 个任务，并发可配置；默认不限速，支持所有下载任务合计的服务级限速。重启后保留任务与检查点，对满足权限、凭据和检查点条件的下载自动恢复。

### 非目标

- 不重新建设 HTTP 服务、百度协议客户端、认证体系或通用任务平台；不另起 RPC 框架。
- 不开发 MoviePilot 插件、下载器适配、资源搜索、订阅、文件浏览或选集界面。MoviePilot 改造由其他需求承接，本需求以容器间 HTTP 下载及文件交付验收。
- 不实现不转存的分享直下，不自动清理网盘副本，不承诺下载提速或所有百度账号、分享类型均可用。
- 不开发登录网页，不将 CLI 当前账号、工作目录或配置全局对象直接共享给 HTTP 服务。
- 不自动恢复结果未知的转存、上传、删除等远端写操作，不自动继续用户明确暂停或取消的任务。
- 不增加新的 Go 第三方依赖，不更换 SQLite、不迁移现有服务数据库格式，不修改普通 CLI 的业务行为。
- 本次写作仅交付 SPEC；产品开发、NAS 变更、提交和推送均不属于本次文档请求。

### 已确认事项与实现假设

- Q18 已确认业务边界，Q13—Q17 的最终修订为：取消删除临时文件、任务并发默认 3、默认不限速、支持幂等、同目录 `.part` 和任务目录隔离。
- 默认 3 指本次交付的 NAS 部署配置。为兼容既有 HTTP 部署，新增启动字段省略时保留原有 2 个执行任务、16 个等待任务及显式恢复行为；NAS 模板显式开启 3 并发和下载自动恢复。
- 下载目录隔离通过已有 `destination.directory` 实现：调用方使用与请求标识稳定关联的唯一子目录，服务负责在该目录下创建和下载文件。NAS 文档与调用示例必须遵守此约定；不隐式改写旧 API 请求的目标路径。
- MoviePilot 当前监听子目录、实际运行 UID/GID 和 DSM ACL 尚未核实。下载路径作为部署参数，实施前只读确认后填入；不能把 `/media` 挂载事实当作任意子目录已受监听的证据。
- 扫码操作仍由用户完成。缺少真实登录或分享资源时，分别记录受控测试和真实验证缺口，不能据此宣告真实链路通过。

## 现状与权威基线

基线为 `main` 的 `85583ffaff241ba51089ec74eac47b3b320bf421`。2026-11-06 已通过远端引用核对确认其与 `origin/main` 一致。该提交已合并 HTTP API 和原生扫码登录；此前基于旧主检出的“缺少 HTTP 服务”结论撤回。

| 能力 | 主干事实与证据 | 本次处理 |
|---|---|---|
| HTTP 服务 | `main.go` 已注册 `server`；内嵌 OpenAPI 为 `0.7.0-p7`，89 条路径、105 个操作；P7 记录为 105 implemented | 复用，不重建；实现数量不等于全部真实百度验证通过 |
| 认证与存储 | `internal/bootstrap/server.go` 已装配 Bearer、独立数据目录、主密钥、SQLite 和目录锁 | 配置持久挂载与权限 |
| 任务与幂等 | `internal/application/transfers/service.go` 已有持久接纳、调度、控制、重放与 `IDEMPOTENCY_KEY_REUSED` | 复用，保留输入及凭据/配置版本绑定 |
| 转存后下载 | `internal/httpapi/p5.go` 支持 `download_after`；bootstrap 将确认成功的转存路径提交为关联下载子任务 | 使用父子任务契约，不以转存父任务完成代替下载完成 |
| 文件发布与取消 | `internal/infrastructure/baidu/transfer.go` 已使用目标目录内随机 `.part`、校验和完成发布，并按任务绑定清理取消的临时文件 | 复用；不要求临时文件必须叫 `影片.mkv.part` |
| 任务并发 | 调度器固定 2 个执行位、总接纳预算 18，等待位 16；指标和能力说明也有固定值 | 增加启动配置并统一实际值 |
| 限速 | `transfer_runtime.go` 按任务创建 limiter，任务内连接共享，任务之间各自计速 | 增加服务级下载总限速，保留每任务选项语义 |
| 重启 | running/pausing 变为 interrupted；queued 自动派发；已有 resume 和检查点恢复 | 增加受限的自动下载恢复策略 |
| 登录 | 原生 CLI 与 HTTP 独立存储；已有 HTTP LOG01—LOG06，支持扫码和账号完成写入 | 提供终端入口调用现有登录 API，不假定运行 `qrlogin` 后服务已登录 |
| Docker | 当前受版本控制文件中没有 Dockerfile 或 Compose | 新增部署产物 |

本会话只读核实的 NAS 环境：x86_64、Docker 24.0.2；MoviePilot 为 `ghcr.io/jxxghp/moviepilot:3.2.3`，属于 `moviepilot_default` bridge 网络。MoviePilot、qBittorrent 和 Emby 均有 `/media` → `/volume2/media` 挂载。此证据不是本项目容器已部署、已健康或已能被 MoviePilot 调用的证明。

## 变更范围

| 文件或模块 | 变更与职责 |
|---|---|
| `Dockerfile`、`.dockerignore`（新增） | 按已有 Go 构建方式产出服务和终端登录辅助程序；运行镜像提供可信 CA；排除凭据、私有配置、验证资源与开发缓存 |
| `deploy/nas/compose.yaml`、启动配置与非秘密参数示例（新增） | 独立服务、共享网络、持久目录、媒体目录、运行身份、健康检查与重启策略；不定义 `ports` |
| `docs/nas-docker-deployment.md`（新增）、`README.md`、`docs/README.md` | 给出构建、首次配置、终端扫码、HTTP 下载、任务目录、取消、恢复、升级和停机操作；仅增加相关导航 |
| `cmd/baidupcs-http-login/`（新增） | 容器内终端扫码客户端，只调用现有 HTTP 登录接口和复用二维码展示能力；不实现另一套百度登录协议 |
| `internal/bootstrap/server.go` 及对应测试 | 新启动字段、参数验证、依赖装配、恢复所需当前权限、实际能力信息；不让 CFG02 修改服务边界 |
| `internal/application/transfers/` 及对应测试 | 可配置执行位、固定等待位、有界恢复、取消与幂等兼容 |
| `internal/infrastructure/baidu/transfer_runtime.go`、相关传输装配与测试 | 下载连接叠加共享服务 limiter；复用现有任务 limiter、检查点和发布逻辑 |
| `internal/domain/metrics/`、相关 capability/契约示例 | 执行容量、队列容量和相关实际限制与配置一致；仅在实际响应变化时更新 OpenAPI 示例和覆盖清单 |
| `internal/bootstrap/`、`internal/httpapi/`、相关基础设施测试、`.github/workflows/http-api.yml` | 增量 HTTP/进程/配置兼容验收，终端登录工具验证，以及既有 14 组契约与恢复检查 |

范围挑战：实施预计超过 10 个文件，因为容器化、启动装配、调度/限速/恢复、终端登录、指标和回归证据分别有已有边界。采用这些边界内的增量修改，不新增 HTTP 服务、不重写传输核心、不把本需求扩大为 MoviePilot 开发。

## 实现说明

### 1. 容器与 NAS 配置

- Docker 构建沿用 `go.mod` 与 HTTP CI 的受支持工具链，保持 `CGO_ENABLED=0`。实施时固定镜像版本及构建来源，不能依赖未声明的 NAS 本机 Go 环境。
- 服务进程作为容器主进程接收 SIGTERM；沿用现有关闭排空、检查点保存和目录锁。容器停止宽限须大于既有 HTTP 与任务关闭窗口，模板采用 60 秒。
- 容器内监听 `0.0.0.0:5299`，加入已存在的外部网络 `moviepilot_default`，服务名采用 `baidupcs`。不映射 NAS 宿主机端口；网络仍需允许服务出站访问百度 HTTPS。
- 建议宿主部署目录为 `/volume1/docker/baidupcs-go`，独立挂载服务数据、只读启动配置及媒体目录。媒体继续映射 `/volume2/media` → `/media`；服务私有数据不能注册为下载 root。
- 服务数据目录沿用 0700，身份/Token/密钥文件沿用 0600。配置容器运行身份和 NAS 目录权限，使 MoviePilot 的实际运行用户能够读取完成文件；不能通过放宽私有数据权限或递归修改整个媒体库解决。
- 下载 root.path 必须指向配置后的监听下载目录。目录需存在；不得写死未核实的监听子目录。每任务目录以 `root_id + directory` 表达，调用方据部署中的 `/media` 映射取得完成文件位置，不新增宿主机绝对路径泄露字段。
- Compose 健康检查复用 `/readyz`，并提供与镜像实际工具匹配的探测方式；running、healthy、账号已登录、真实下载成功分别记录。
- 构建上下文不得包含 `.env`、已有 `pcs_config.json`、服务数据、Cookie、二维码或真实上游验证输入。Token 不写入 Compose 明文、镜像层或健康检查命令。

### 2. 启动配置与兼容

复用 `server --config`，新增三个启动字段，不新增在线调度配置接口：

| 字段 | 类型与约束 | 省略时的兼容行为 | NAS 模板值 |
|---|---|---|---|
| `max_running_jobs` | 整数，1—16 | 2 | 3 |
| `download_rate_limit_bytes_per_second` | int64 范围内的非负十进制字符串；`"0"` 为不限速 | `"0"` | `"0"` |
| `resume_downloads_on_startup` | 布尔值 | false | true |

字段名和边界属于本 SPEC 的可回退实现选择。配置错误应在派发任务和产生业务副作用前退出，不能静默回退；修改上述启动参数需要重启。启动配置重载检查同样识别这些字段，不能借 CFG06 热更新它们。

继续保留现有 `client.max_download_rate` 和单次下载的 `options.max_rate_bytes_per_second`，二者仍表示提交时冻结的任务限速。服务总限速为实例启动边界，不写回任务历史配置，也不改变旧请求的幂等摘要。

### 3. 调度与总限速

- 复用现有 dispatcher；等待容量保持 16，总接纳预算为 `max_running_jobs + 16`。领取、恢复、队列满判断、关闭和目标锁必须使用同一实际预算，不保留散落的 2/18 常量。
- NAS 模板下最多 3 个任务同时执行，第 4 个排队；调低或调高并发只在重启后生效，不改变已经持久化的任务身份。
- 共享下载 limiter 覆盖该服务实例内所有下载任务、单任务内文件及连接。它与已有任务 limiter 同时约束，等待支持取消，避免取消时因限速等待无法退出。
- 不把上传流量、登录或普通元数据请求计入下载总速率；不要求每个连接各获得一份完整的总带宽额度。
- 总限速为 0 时不引入节流等待。有限速测试使用足够长的采样窗口、明确容差及真实传输字节，不能仅断言配置值。
- `/metrics` 的执行/等待容量以及 capabilities 中相关限制反映实际装配值，指标标签继续有界，不包含路径、任务 ID 或凭据。

### 4. 自动恢复边界

- 开启恢复后，复用现有持久记录、检查点、resume 校验和派发流程；queued 延续现有自动调度行为。只恢复符合条件的 download，不把所有 interrupted 统一改回 queued。
- 自动恢复须验证当前身份仍存在且具备原账号/root 权限，凭据版本仍可使用，目标仍在授权目录，检查点与冻结的源、目标和配置匹配。不能伪造 owner 或借管理员身份扩大原任务权限。
- 手动 paused、cancelled、已完成、缺失/撤销凭据、检查点不匹配以及源已变化的任务不自动运行；API 保留原状态或明确问题，不能显示成功。
- 处于 committing/done 或提交结果未知的任务保留核验边界。只有通过既有本地结果核验、确认不会重复覆盖或重放副作用后才能继续；不能自动重发未知转存等远端写操作。
- 恢复沿用原 job_id、事件序号、配置/凭据绑定和幂等记录；不调用 retry 新建替代任务，不重复创建转存父任务及下载子任务。
- 恢复候选按有界批次接纳，超过当前容量的候选保留持久记录并随容量释放继续检查，不丢失、不绕过队列限制；瞬时存储错误要可观察，不无限忙循环。
- 凭据过期时由用户重新登录。普通凭据更新与撤销继续遵守现有版本规则，不默默把旧任务绑定到另一个账号或新凭据版本。

### 5. 登录、任务提交与文件交付

- 终端登录工具在同容器中调用 loopback 的 LOG01—LOG05：创建 qrcode 会话、取得二维码、在终端展示、轮询确认、完成到服务账号。首次建立账号；重新登录时显式指定待更新的 account_id，沿用身份核验。
- 工具从私有 Token 文件读取凭据，不要求把 Token、Cookie 或票据写在命令行，不输出完整认证 URL、原始登录响应或 Cookie。取消、超时及协议失败返回明确结果并释放会话；不要求部署登录网页。
- `qrlogin` 仍属于 CLI 独立配置流程。文档说明这种差异；本需求首选终端工具调用已有 HTTP 登录，不修改旧账号导入器或假定结构化 Cookie 可以无损降为任意 Cookie 字符串。
- 网盘路径使用已有下载 API。分享使用 `download_after=true` 和专用网盘目标目录；下载子任务仅包含确认转存成功的项。调用示例必须展示 parent_job_id/child_job_ids 跟踪，只有下载子任务完成才视为文件已交付。
- 每次业务提交使用稳定的 `Idempotency-Key` 与稳定任务子目录。同标识、同输入重放原任务；同标识改变目录或资源返回现有 409；不同标识可创建新任务。
- 直接在配置的监听目录及其任务子目录中写现有随机 `.part`，单文件下载及选定校验完成后发布原文件名。一个文件完成即可被监听，任务整体状态仍按所有项结果汇总。
- 确认 MoviePilot 配置忽略 `.part`。本次验收证明未完成文件不可作为完成媒体读取、完成文件可读取，不声称 MoviePilot 插件或媒体整理功能已经交付。
- 取消 API 的 202 代表控制意图；待执行器停止并完成清理后才确认 cancelled。删除仅限该下载任务绑定的临时数据，保留完成文件、其他任务数据及网盘副本。
- 分享父任务完成不等于其下载子任务完成，也不意味着取消父任务会级联取消已运行的子任务。保留既有语义，操作说明指向实际下载子任务，不新增级联取消契约。

### 6. 持久化、接口与升级

- 本次不新增数据库表或 schema 版本；执行容量、总限速和恢复开关作为启动配置传入已有组件。
- 继续使用既有 API 路径、操作 ID、任务状态、错误模型、retry/resume 区别和权限检查。确需更新实际限制元数据时，同步示例、覆盖矩阵及真实响应验证，不批量重生成清单掩盖变化。
- 服务数据目录及 `master.key`、SQLite/WAL、身份文件、artifacts/staging 是完整持久化单位，不能只挂载一个数据库文件；多实例不能共享同一可写数据目录。
- 部署升级和回滚沿用 P7 停机与配套备份流程，不自行清空数据。回滚服务前移除旧二进制不认识的新启动字段，恢复旧执行预算与手动恢复行为；已经发生的百度副作用不能回滚。

## 外部参照与设计取舍

本需求沿用仓库已验收的技术栈、存储和 HTTP 协议，不引入新的难以回退选型；不以外部方案覆盖主干事实。对照材料为 P4/P5/P6/P7 指南、现有 HTTP CI、主干实现和本会话 NAS 只读检查；没有将分享直下的外部实现纳入本需求。

| 议题 | 最小可行方案 | 长期方案或替代方案 | 本次选择与理由 | 确认方式 |
|---|---|---|---|---|
| 服务建设 | 容器化现有 server，补齐目标差异 | 新建完整 HTTP/任务平台 | 复用现有服务，避免重复开发及契约分叉 | 用户确认 |
| 交付边界 | 本项目部署、API 和操作说明 | 同时改造 MoviePilot | MoviePilot 由其他需求承接 | 用户确认 |
| 分享下载 | 自动转存后创建下载子任务 | 增加不转存分享直下协议 | 首版使用现有受验证链路，专用目录保留副本 | 用户确认 |
| 文件交付 | 同目录临时后缀、单文件完成发布、独立任务目录 | 监听目录外暂存整批后交付 | 采用同目录方案及现有目标目录 API | 用户确认 |
| 账号与网络 | 单账号、终端人工扫码、Bearer、Docker 共享网络 | 多用户门户、公网入口 | 使用已确认范围；终端工具复用已有 HTTP 登录 | 用户确认 |
| 任务容量与限速 | 在已有 dispatcher/limiter 中增加启动参数 | 引入新调度服务、外部队列或统一下载平台 | 原有模块内增量适配，NAS 默认 3 且不限速 | 用户确认 |
| 自动恢复 | 下载检查点和权限通过后恢复 | 无条件重放所有任务 | 保留未知副作用核验及手动暂停语义 | 用户确认 |

实现选择：启动字段命名、旧部署缺省兼容、终端工具文件位置和候选分批策略由本 SPEC 明示；不构成新增业务范围或要求用户重新确认既有授权。若实施发现必须改变数据库、权限模型或已确认行为，应更新 SPEC 后按项目规则处理。

## 验收标准

| 编号 | 输入或用户操作位置 | 可观察结果 | 验证证据 |
|---|---|---|---|
| AC01 | 使用交付的 Docker/Compose 构建并在当前 NAS 启动 | 独立容器运行且健康；数据与下载目录按配置持久挂载；没有宿主端口发布；容器可出站访问百度 HTTPS | 镜像构建、Compose 检查、筛选后的 inspect、健康检查与连接证据 |
| AC02 | 同 `moviepilot_default` 的验证容器请求 API | 服务名可解析；无 Token 返回 401，合法 Token 可读 system/capabilities 并提交测试下载；非共享网络没有新增对外服务入口 | 实际 HTTP 状态与 Docker 网络/端口检查，不向其他应用发送消息 |
| AC03 | 容器终端发起扫码、用户确认，随后重启服务 | 工具显示明确成功和 account_id；API 身份核验可用；重启后无需再次导入 Cookie；拒绝、取消、超时不覆盖有效账号 | 受控登录流程与实际终端结果；真实扫码是否完成单独标注 |
| AC04 | 提交网盘文件/目录，或带提取码的分享并开启下载 | 返回 202 及任务 ID；分享转存到配置的专用目录并关联下载子任务；结果保留原文件名和结构；进度、逐项错误可查 | 生产 handler、受控百度接口及授权真实小文件证据 |
| AC05 | 向配置监听目录下载两个并行、存在同名文件的业务任务 | 使用不同的稳定子目录；传输期间仅有任务绑定 `.part`；每个文件校验后发布原名；MoviePilot 实际运行身份可读完成内容 | 文件系统观测、摘要比对、以目标身份读取；不以插件整理成功作为本次门槛 |
| AC06 | 取消实际正在下载的子任务 | 202 后可观察到收尾；停止写入后该任务临时文件消失，完成文件、另一任务文件和网盘副本仍在；重启不复活取消任务 | 真实 handler 控制、文件断言、重启边界测试 |
| AC07 | 同 key/同请求并发提交及重启后重放；同 key 改输入 | 同请求得到原任务 ID，无重复转存/下载；改变资源或目录返回现有 409 | SQLite 持久幂等、并发及父子任务回归测试 |
| AC08 | NAS 配置提交 4 个可控阻塞任务，再将执行上限分别改为 1 和 3 重启 | 模板默认最多 3 个执行，第 4 个等待；显式配置为 1 时最多 1 个；等待 16、总预算随执行位变化；指标/能力一致；省略新字段保持旧缺省 | 控制执行器、真实 HTTP、指标断言；无效配置启动失败 |
| AC09 | 多个任务并行下载，配置服务总限速，再设为 0 | 全部任务字节合计速率在规定窗口/容差内受限；单任务限速仍单独生效；设为 0 不增加总节流；限速等待中取消可收尾 | 带宽集成测试及可重复采样报告 |
| AC10 | 有已确认检查点的下载运行中停止/杀进程，再按 NAS 模板重启 | 任务 ID 与事件序号保留，符合条件的下载自动从确认位置继续；排队任务不丢；候选超过容量时有界恢复 | 真实服务进程、Range 请求与逐字节内容断言；SIGTERM/SIGKILL 分别记录 |
| AC11 | 对暂停、已取消、失去权限/凭据、源改变及提交未知的任务执行相同重启 | 不自动执行或冒充成功；保留明确状态和问题；未知转存不重复提交；禁用自动恢复时保持旧行为 | 负向恢复、当前权限、凭据换版、未知提交 fixture |
| AC12 | 不使用新启动字段运行旧 API/CLI 回归、P7 契约与恢复检查 | 原路径、状态、幂等、权限、检查点与备份恢复语义保留；新增工具不初始化日常 CLI 配置；输出不含凭据 | HTTP CI、CLI 聚焦检查、恢复/构建检查及脱敏证据 |

AC03—AC05 中需要百度账号或分享的步骤不能伪造资源。真实验证缺少必要输入时列明未完成项，受控上游测试可以覆盖协议和故障分支，不能替代真实 NAS 下载成功结论。

## 验证计划

### 当前 SPEC 文档检查

```sh
bash .claude/skills/spec/scripts/spec-lint.sh docs/astack/version/261106-gsb_NasDockerDeployment_SPEC.md
git diff --check
python3 .claude/skills/spec/scripts/spec_workflow.py list --root . --all
```

另以定向搜索检查源文件、接口名、旧路径和“新建 HTTP 服务”等过期结论；检查文档引用都指向现有资料，新增交付路径明确标为新增。

### 实施阶段质量门

以下均取自现有项目入口或 `.github/workflows/http-api.yml`，本次写 SPEC 不执行产品验收。新增终端工具的测试与构建目标在文件创建后补入 CI，并将实际命令追加到验证记录。

```sh
go vet ./internal/pcslogin ./internal/pcsconfig ./internal/pcscommand ./requester ./internal/bootstrap ./internal/application/transfers ./internal/infrastructure/baidu ./internal/httpapi
go test ./internal/pcslogin ./internal/pcsconfig ./internal/pcscommand ./requester
CGO_ENABLED=0 go build -trimpath -ldflags '-w -s' -o /tmp/BaiduPCS-Go .
```

使用现有锁定的 Python 校验依赖，按 HTTP CI 导出完整 14 组生产 handler 响应：

```sh
BAIDUPCS_P1_CONTRACT_RESPONSES=/tmp/baidupcs-p1-responses.json \
BAIDUPCS_P2_CONTRACT_RESPONSES=/tmp/baidupcs-p2-responses.json \
BAIDUPCS_P3_ACCOUNT_RESPONSES=/tmp/baidupcs-p3-accounts.json \
BAIDUPCS_P3_CONTRACT_RESPONSES=/tmp/baidupcs-p3-responses.json \
BAIDUPCS_P4_CONTRACT_RESPONSES=/tmp/baidupcs-p4-responses.json \
BAIDUPCS_P5_FILE_RESPONSES=/tmp/baidupcs-p5-files.json \
BAIDUPCS_P5_SHARE_RESPONSES=/tmp/baidupcs-p5-shares.json \
BAIDUPCS_P5_BUSINESS_RESPONSES=/tmp/baidupcs-p5-business.json \
BAIDUPCS_P5_RECONCILE_RESPONSES=/tmp/baidupcs-p5-reconcile.json \
BAIDUPCS_P6_TOOL_RESPONSES=/tmp/baidupcs-p6-tools.json \
BAIDUPCS_P6_LOGIN_RESPONSES=/tmp/baidupcs-p6-login.json \
BAIDUPCS_P6_MANAGEMENT_RESPONSES=/tmp/baidupcs-p6-management.json \
BAIDUPCS_P6_RECEPTION_RESPONSES=/tmp/baidupcs-p6-receptions.json \
BAIDUPCS_P7_METRICS_RESPONSES=/tmp/baidupcs-p7-metrics.json \
go test -p=1 -race -count=1 -timeout=600s \
  ./internal/domain/... ./internal/application/... \
  ./internal/infrastructure/... ./internal/httpapi ./internal/bootstrap . ./baidupcs

python3 api/openapi/tools/check_contract.py --live-responses \
  /tmp/baidupcs-p1-responses.json /tmp/baidupcs-p2-responses.json \
  /tmp/baidupcs-p3-accounts.json /tmp/baidupcs-p3-responses.json \
  /tmp/baidupcs-p4-responses.json /tmp/baidupcs-p5-files.json \
  /tmp/baidupcs-p5-shares.json /tmp/baidupcs-p5-business.json \
  /tmp/baidupcs-p5-reconcile.json /tmp/baidupcs-p6-tools.json \
  /tmp/baidupcs-p6-login.json /tmp/baidupcs-p6-management.json \
  /tmp/baidupcs-p6-receptions.json /tmp/baidupcs-p7-metrics.json

go test -count=1 -timeout=120s \
  ./pcsliner/args ./pcsutil/cachepool ./pcsutil/converter \
  ./pcsutil/escaper ./pcsutil/waitgroup ./pcsutil/taskframework \
  ./baidupcs/netdisksign ./baidupcs/expires/cachemap \
  ./requester ./requester/rio ./requester/rio/speeds \
  ./requester/uploader ./internal/pcscommand ./internal/pcsfunctions/pcsupload

python3 api/openapi/tools/check_recovery.py --report /tmp/baidupcs-nas-recovery.json
python3 api/openapi/tools/check_builds.py
```

- 新增有判别力的配置、并发容量、跨任务限速、自动恢复、终端登录和取消清理测试；在修改实现前或通过负向运行证明缺失实现时测试会失败，不改弱旧断言来适配新默认值。
- 本次涉及共享模块，实施时执行 `go test ./...` 并分类记录历史 downloader 编译、开发者绝对路径、外网示例等问题；不把聚焦或 HTTP 质量门通过写成全量通过。
- AC01—AC11 的容器专项需实际镜像构建、Compose 校验、同网 HTTP 请求、文件观测和容器重启。实施时通过 nas-operator 使用 NAS 官方 Docker/Compose，记录实际生成文件路径和完整脱敏命令；当前不冒充这些部署文件或验收脚本已经存在。
- 独立 Review：需要，在实施完成后、任何 ship 前执行。原因是跨模块、恢复状态、认证边界、文件删除和已有契约兼容均受影响。按 `open-code-review-delegate` 的适用流程记录覆盖范围、结论和必要复验；若技能不可用则明确记录评审缺口，不用开发自查代替。当前仅 SPEC 校验不宣称完成实施 Review。

## 验证记录

| 日期 | 命令或证据 | 结果 | 备注 |
|---|---|---|---|
| 2026-11-06 | `git log -1`；既有 `git ls-remote origin refs/heads/main` 核查 | 基线确认 | 主干与远端均为 `85583ff`；未改 Git 历史 |
| 2026-11-06 | 读取内嵌 OpenAPI、P7 记录及相关主干代码 | 现状确认 | 105 个操作、89 条路径；HTTP/任务/临时发布已有，固定并发、任务级限速及显式恢复为差异；不是本轮测试通过记录 |
| 2026-11-06 | `spec-lint.sh docs/astack/version/261106-gsb_NasDockerDeployment_SPEC.md` | 通过 | Spec Lint PASS，0 warnings |
| 2026-11-06 | `git diff --check`；新增文档 `git diff --no-index --check`；`spec_workflow.py list --root . --all` | 通过 | 新需求可被索引，状态为待实施；新增文件无空白错误 |
| 2026-11-06 | 本地 Markdown 链接、必填章节、shell 命令语法和现有验证入口检查 | 通过 | 初检纠正指标模块为 `internal/domain/metrics/`，随后复验；2 个本地文档链接有效，七个必填章节齐全；实施验证命令仅做静态检查，未执行 Go 测试或 NAS 变更 |
| 2026-11-06 | Go/HTTP CI、终端工具、容器与真实 NAS 验收 | 待执行 | 本次请求仅写 SPEC；不得将 P7 历史证据或 NAS 只读检查当成本需求交付成功 |
| 2026-11-06 | 独立 Review | 待执行 | 实施结束后执行，本次文档阶段不运行 |

## 变更日志

- 2026-11-06：依据 Q1—Q18 的最终确认及最新主干核查创建 SPEC；明确复用 P7 HTTP 服务，新增 NAS 容器化、启动调度配置、总限速、受限自动恢复及终端登录衔接；记录兼容边界与可执行验证入口。
