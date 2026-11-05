# HTTP API 契约与验证

日期：2026-11-05。版本：`0.3.0-p3`。状态：40 implemented、3 partial、62 planned；[P3指南](../../docs/http-api-p3-guide.md)和[逐项验收](../../docs/http-api-p3-verification.md)记录完整账号、文件读取/扫描、任务/事件及结果文件。UPL01/DWN01/JOB08保留P2子集，完整传输在P4。P2真实百度证据保留其原账号/时间/范围；本轮实测另列。原业务基线 `1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73`，P0/P1历史清单保留在baselines/。

[openapi.yaml](openapi.yaml) 是 HTTP 字段、类型、约束和 operationId 的唯一契约源。共 105 个 operation、89 个 path、190 个 schema。operationId 固定使用设计中的 SYS01、ACC01 等 ID。`/api/v1/openapi.json` 已在 P1 原样提供由 YAML 生成的内嵌 JSON。

| 资料 | 用途 |
| --- | --- |
| [工程交接](../../docs/http-api-handoff.md) | P3交付、从P4继续的代码落点、边界和完整阶段核销 |
| [设计](../../docs/http-api-design.md) | 分层、领域、依赖和流程 |
| [覆盖矩阵](../../docs/http-api-coverage.md) | 逐项人工映射和源码证据 |
| [P0 核对与基线报告](../../docs/http-api-p0-baseline.md) | 修正结论、已有失败、平台和存储探测 |
| [source-inventory.json](source-inventory.json) | 当前616条AST清单、源码哈希、映射和处理分类；[P0冻结清单](baselines/p0-source-inventory.json)保留原609条，P1清单另存baselines/p1-source-inventory.json |
| [http-exchanges.json](examples/http-exchanges.json) | 全部 105 个操作的合成 HTTP 请求与响应，含头、流和二进制示例 |
| [requests.http](examples/requests.http) | 常见读取、上传下载、SSE、ETag、Range 和票据调用格式 |
| [schema-cases.json](examples/schema-cases.json) | 67 个可执行的 schema 正反例 |
| [behavior-cases.json](examples/behavior-cases.json) | 25 组行为规格；5组链接到已运行离线测试，其余按后续阶段核销 |
| [job-events.sse](examples/job-events.sse) | 快照、进度、终态和注释心跳的实际 SSE 帧 |

所有示例均为合成数据，`EXAMPLE_ONLY` 不是有效凭据；示例之间各自独立，不表示已经创建资源。图像响应中的 1×1 PNG 仅演示媒体类型，不是登录二维码。已实现的P1操作可以按启动指南实际调用，未来操作的示例保留为目标契约。

## 校验和重新生成清单

从仓库根目录执行。Python 3.10+ 和 Go 1.23+ 可运行工具；本次实际使用 Python 3.14.7、Go 1.23.12/1.26.2。校验工具仅安装到独立环境，不进入 `go.mod`。

```sh
python3 -m venv /tmp/baidupcs-http-p0-venv
/tmp/baidupcs-http-p0-venv/bin/python -m pip install -r api/openapi/tools/requirements.txt
/tmp/baidupcs-http-p0-venv/bin/python api/openapi/tools/check_contract.py
```

校验器固定 `openapi-spec-validator==0.7.2`、`jsonschema==4.23.0`、`PyYAML==6.0.2`，已实际验证 OpenAPI **3.1.1**，包括 `oneOf`、条件约束、null 联合类型和 `unevaluatedProperties`。工具安装、首次下载 Go 工具链或 SQLite 探测需要网络；安装后的契约检查仅访问本地契约和源码。不引入Go业务代码生成器；P1手写DTO通过真实HTTP响应验收。openapi.json由export_contract.py生成并嵌入服务，校验器核对它与YAML一致，不把3.1降级成3.0。

检查覆盖：重复 YAML key、OpenAPI 结构、本地 `$ref`、schema 合法性、设计路由及阶段逐项一致、path 参数、认证与权限决策、幂等头、ETag 错误、任务 202、HEAD/204 空 body、全部内嵌示例、外部正反例、SSE 帧、全量 HTTP exchanges，以及当前 AST 与人工矩阵/持久清单的一致性。行为规格检查确认操作和测试函数引用有效；5组有独立Go运行证据，链接检查本身不计为测试执行。

升级上游时先查看新 AST 和源码差异，再更新人工矩阵与契约，最后重建已分类清单：

```sh
go run api/openapi/tools/inventory.go > /tmp/baidupcs-http-observed.json
/tmp/baidupcs-http-p0-venv/bin/python api/openapi/tools/check_contract.py --write-inventory
/tmp/baidupcs-http-p0-venv/bin/python api/openapi/tools/check_contract.py
```

缺少人工映射时重建会失败；不能用更新数量掩盖遗漏。AST 工具只解析代码，不导入、执行或初始化 CLI。当前包含404个导出函数/方法、52个命令节点、90个命令参数、1个全局参数、24个配置字段和45个业务选项字段，共616条；P1增量为server命令及4个启动参数，P2新增一个内部ExecuteContext原语，P3新增共用解析的FilesDirectoriesListPage。签名、缓存、网络和通用工具子包另由覆盖矩阵的基础设施归属表管理。

## 公共协议与待验证目标

已实现 P1 的语义及公共身份/路径/错误/数值编码保持稳定。planned 操作是覆盖目标，字段与状态可依据 P2 实际传输验证修订；必须同步 YAML、设计和示例。x-phase 表示完整验收阶段，x-early-validation=P2 表示受限路径提前验证，不表示整个操作已实现。管理能力和扩展按调整后的实施计划后置。

| 项目 | 决定 |
| --- | --- |
| 成功 JSON | `{data,meta}`；meta.request_id 与 X-Request-ID 相同；空集合用 `[]` |
| 原样内容 | OpenAPI、HTML、Prometheus、SSE、PNG 和文件内容不包 data/meta；DELETE 成功 204 无 body |
| 整数与时间 | 百度数字 ID、大小、速率、累计数量和 revision 用十进制字符串；有界页码/并发/重试/天数用 number；时间为 UTC RFC 3339，未知值用 null |
| 凭据导入 | `credentials.kind=bduss\|cookies\|access_token` 三选一；单独 Cookie 字段只随 bduss 类型导入；更新时缺失保留、null 清除、空字符串拒绝 |
| Cookie 更新一致性 | 只有一个规范化 Jar；cookies 字符串替换整个 Jar，cookies=null 清除 Cookie 类凭据并保留 access_token；单字段更新/清除同步 Jar，旧文本不能恢复已删除字段；缺 BDUSS/冲突 Cookie 为 400 INVALID_CREDENTIAL_FORMAT |
| 密码与 Cookie | 只写；分享提取码是独立的业务输出，SHR 响应可以按权限返回；秘密不得进入 job、事件或日志 |
| 网盘路径 | 缺省 `/`；访问网盘的相对路径必须提供 base_path 或自己的 context_id；两者同时出现 400；context 账号不一致 409 |
| 本地路径 | 文件引用统一 `root_id + relative_path`；目标目录统一 `root_id + directory`；检查实际路径和软链接，禁止用服务器 cwd 隐式解释 |
| selector | `kind=paths\|fs_ids\|patterns`，values 非空；按字面量处理 paths 中的 `*`；提交时固定选择，不在排队后重算 |
| 文件分页 | fields 默认 all；page 从 1 开始，page_size 默认 100、最大 1000；total/has_more 可以 null；fields 只投影非必填字段；summary 只统计本页 |
| 上游固定分页 | 回收站每页 100；分享不开放 page_size，无法确认上游页容量时 meta.pagination.page_size=null |
| 快照分页 | 搜索、job列表、分享检查、离线列表用 opaque cursor；扫描job/item用固定高水位游标；绑定查询、owner、账号；搜索参数改变 409、过期 410 |
| 离线列表 | 主项目固定 `start=0&limit=1000`；服务快照只覆盖实际收到的集合，不能宣称已获得所有历史任务 |
| 批量写入 | 包括 OFF01 批量创建及 OFF06 清空，统一返回 202 job；百度 offline_task_id 在最终 result.items，不与 job_id 混用 |
| 批量查询 | 保留每个输入的成功或失败；OFF03 最多 100 个唯一 ID，101 项直接 413，不能静默截断 |
| 选项位置 | 扫描、上传下载、转存、导出和校验的执行选项统一在 `options` 内；身份、来源、选择和目标在请求顶层 |
| 默认值 | 请求缺失字段才继承提交时配置；false、0、空代理字符串分别有意义；no_check/ignore_illegal 做反向映射；Job.effective_options 使用单独 Effective* schema，强制完整返回生效字段 |
| 上传源 | `type=local` 使用 relative_path；`type=staged` 使用 staged_file_id；接收断点与到百度的断点分开 |
| 下载目标 | relative 单文件用 basename，单目录保留根名；多根去重并移除重叠子项，再按选择根父目录的最近公共祖先保留路径；full 保留完整网盘路径；冲突显式报告；初版没有 flat |
| test 与 dry_run | test 实际传输但不写最终文件；dry_run 仍创建 job，只固定并返回计划，不传输或修改数据 |
| 版本比较 | ACC04/ACC06、CTX03、CFG02/CFG05/CFG06/CFG09 必须 If-Match，格式为强 ETag `"7"`；缺少 428，不匹配 412；不接受 `*` |
| 幂等 | 标为 supported 的写操作支持可选 Idempotency-Key；同输入重放首次状态/响应，同 key 不同输入 409；摘要含路径参数，不只含 body |
| 重复任务控制 | 带相同幂等 key 时重放优先；不带 key 的重复控制返回当前 job。202 表示接受控制意图，不等于已暂停或已取消 |
| 重试与恢复 | retry 创建新 job；resume 延续原 job；远端结果未知的操作先 reconcile，禁止盲目重放 |
| SSE | event 类型包括 job.snapshot、job.created、job.state_changed、job.progress、item.completed、job.finished；心跳为无 ID 注释；无游标先快照再跟随 |
| SSE 游标 | 过期 410，跨 job/格式错误 400，不存在的未来游标 409；序号持久化、单 job 单调，客户端去重；token 放 Authorization |
| Range/HEAD | 单 range，语法错误或多范围 400，不满足 416；If-Range 不匹配返回完整 200；HEAD 所有状态都无 body，错误 code 在 X-Error-Code |
| 票据兑换 | DWN05 返回的 URL 指向 DWN03/04，短期 query ticket 是该文件的受限 Bearer 凭据；不需长期 token，仍复核原 owner 权限；不可授权其他操作 |
| 分享检查 | 首次 input；后续 inspection_id 支持 directory_fs_id、cursor、page_size；fs_ids 只能来自相应分享，不能只选择首批文件 |
| 分享有效期 | 支持任意有效天数，0 永久；不限制为 1/7/30。显式错误提取码拒绝，省略才生成；combined_link 只控制链接表达 |
| 导出根改写 | options.root_path 只改导出内容中的虚拟路径；省略或空串保留原网盘路径，允许相对根，不按 context/cwd 解析或读写目录；legacy_link 忽略路径改写并返回 warning |
| 分享详情 | ShareSURLInfo 只提供短链接和提取码，列表字段未知时省略，不伪造 fs_ids、状态或访问量 |
| 回收站时间 | 保留 upstream_left_time 原值；源码未说明单位，未经上游验证不换算绝对过期时间 |
| 配置 | 22 个客户端字段；savedir 改为受控目录引用，未配置授权默认根时读值为 null；proxy 只写、读值脱敏；旧 u_policy 仅导入时兼容 |
| 配置重载 | 只重载客户端字段，不借此导入账号或凭据；dry_run 不保存；来源由启动时固定；监听/TLS/密钥/存储路径变更需重启并返回 422 |
| 网络默认值 | 版本化客户端使用显式配置；不在请求中修改 requester 全局变量。代理主机规则使用精确主机或 `*.domain`，不沿用子串匹配；无显式代理时不隐式继承进程代理环境 |
| 历史能力 | no_split_file=true、randomize_name=true 返回 422；INT06 合法请求默认 503 UPSTREAM_CAPABILITY_UNAVAILABLE；未实现操作 501；存在函数不等于上游可用 |

路径、权限、状态、数值范围和可用能力中的部分约束需要应用服务执行；schema 校验通过不代表业务请求可以执行。`x-authorization` 是各操作所需 scope 和资源条件；所有 scope 按 AND 检查，admin 可满足业务 scope，但账号 allowlist、root 授权和资源所有权仍须按操作检查。UPL01 的本地来源另需 local.read；DWN01 另需 local.write；转存后下载另需 local.write。普通调用方只能查看和控制自己的 job/资源，admin 仅在明示的管理或 owner_or_admin 条件下跨 owner。

## 限制、默认值与 HTTP 错误

OpenAPI 固定字段格式、字符串长度、集合上限及有界数值。应用层补充校验：int64 字节上限、int64/uint64 百度 ID 上限、CRC32 ≤ 4294967295、cache_size ≥ 1024 且可用平台 int 表达、root/host/IP 有效性、凭据兼容性和路径归属。格式/必填错误 400；批量或体积超过限制 413；业务能力不支持 422。

以下为对应能力实现时的默认值；P1仅落实当前启用子系统的限制，未实现项不是已经生效的启动选项；它们不属于 CFG02 的 22 个客户端字段。部署可收紧限制，运行时 capabilities 返回实际限制，不能静默改变一次已接受任务的执行语义。

| 服务行为 | 初始默认值 |
| --- | --- |
| JSON 请求体 | 1 MiB，超过 413；二进制/multipart 独立流式限制 |
| 批量请求 | 100 项；单次响应页最多 1000 项 |
| 查询快照 | 最多 10000 项、TTL 600 秒；超容量 413，改用扫描 job |
| context | TTL 86400 秒，成功更新后续期；过期 410 |
| 登录会话 | TTL 300 秒，不因轮询续期 |
| 分享 inspection | TTL 600 秒，不因读取续期 |
| 暂存文件/接收会话 | 无引用文件或未完成接收闲置 86400 秒后可清理；有活动引用不删除 |
| 单个接收块 | 32 MiB；超限 413，偏移冲突 409 |
| 暂存单文件 | 不超过代码 128 GiB 与部署暂存配额中较小者；空间不足失败不能先返回可用 ID |
| 幂等记录 | 至少 86400 秒；活动 job 绑定至少保留到终态 |
| SSE | 心跳 15 秒；单连接队列 128 帧；每 job 最多保留 10000 个事件或 86400 秒，以先达到者为准 |
| 扫描 | max_nodes 默认 100000，max_duration_seconds 默认 3600；达到限制 complete=false |
| 下载票据 | 默认 60 秒，最大 300 秒；允许有效期内的单 range 重取 |
| 同步上游 | 默认 30 秒；流/SSE 独立空闲超时，不使用统一整请求 WriteTimeout |

| HTTP | 稳定 code 示例 |
| --- | --- |
| 400 | INVALID_REQUEST、RELATIVE_PATH_REQUIRES_SCOPE、AMBIGUOUS_PATH_SCOPE、MULTIPLE_RANGES_UNSUPPORTED |
| 401 / 403 | API_UNAUTHORIZED / FORBIDDEN、LOCAL_PATH_FORBIDDEN |
| 404 | RESOURCE_NOT_FOUND、REMOTE_PATH_NOT_FOUND |
| 409 | STATE_CONFLICT、CONTEXT_ACCOUNT_MISMATCH、IDEMPOTENCY_KEY_REUSED、QUERY_CURSOR_MISMATCH、UPLOAD_OFFSET_MISMATCH、STAGED_FILE_IN_USE、CHECKSUM_MISMATCH、REMOTE_RESULT_UNKNOWN |
| 410 | RESOURCE_EXPIRED、EVENT_CURSOR_EXPIRED、QUERY_SNAPSHOT_EXPIRED |
| 412 / 428 | REVISION_MISMATCH / IF_MATCH_REQUIRED |
| 413 / 415 / 416 | LIMIT_EXCEEDED、QUERY_SNAPSHOT_LIMIT / UNSUPPORTED_MEDIA_TYPE / RANGE_NOT_SATISFIABLE |
| 422 | UNSUPPORTED_OPTION、BAIDU_CREDENTIAL_EXPIRED、CHECKSUM_UNAVAILABLE、STARTUP_CONFIGURATION_CHANGE_REQUIRES_RESTART |
| 429 | RATE_LIMITED，Retry-After |
| 500 / 501 | INTERNAL_ERROR / CAPABILITY_NOT_IMPLEMENTED |
| 502 / 503 / 504 | UPSTREAM_ERROR / UPSTREAM_UNAVAILABLE、UPSTREAM_CAPABILITY_UNAVAILABLE / UPSTREAM_TIMEOUT |

错误采用 `application/problem+json`；百度错误必须结合 operation、ErrType 和远端码适配，不按一个整数做全局映射。503 在适合稍后重试时带 Retry-After；INT06 等长期不可用能力 `retryable=false`，不应诱发循环重试。流开始后只能终止流并记录错误，不追加 JSON。/healthz、/readyz 可匿名且只返回最小状态；readyz 只检查当前启用的运行子系统，不要求百度在线，也不要求尚未开放的阶段能力准备完成。


## P1—P3 实际响应与持续校验

P1—P3测试运行生产handler并导出JSON、SSE、文件内容以及HEAD/204实际响应；P2保留HTTP子进程SIGKILL和专用账号opt-in测试。先安装上述校验依赖，再执行：

```sh
BAIDUPCS_P1_CONTRACT_RESPONSES=/tmp/baidupcs-p1-responses.json \
BAIDUPCS_P2_CONTRACT_RESPONSES=/tmp/baidupcs-p2-responses.json \
BAIDUPCS_P3_ACCOUNT_RESPONSES=/tmp/baidupcs-p3-accounts.json \
BAIDUPCS_P3_CONTRACT_RESPONSES=/tmp/baidupcs-p3-responses.json \
GOTOOLCHAIN=go1.23.12 go test -race -count=1 -timeout=180s \
  ./internal/domain/... ./internal/application/... \
  ./internal/infrastructure/... ./internal/httpapi ./internal/bootstrap .
/tmp/baidupcs-http-p0-venv/bin/python api/openapi/tools/check_contract.py \
  --live-responses /tmp/baidupcs-p1-responses.json /tmp/baidupcs-p2-responses.json /tmp/baidupcs-p3-accounts.json /tmp/baidupcs-p3-responses.json
python3 api/openapi/tools/check_builds.py --report /tmp/baidupcs-p2-builds.json
```

修改YAML后运行 `python3 api/openapi/tools/export_contract.py` 或 `go generate ./api/openapi`，生成JSON再编译。持续校验工作流为 `.github/workflows/http-api.yml`；原构建工作流的目标矩阵保持。

P0已有的25组完整行为规格仍供后续阶段逐项核销；P1的鉴权/配置和P2的受限账号/传输/任务由真实集成测试分别覆盖。P3已在v3上增量迁移到v4，完整读取、任务、事件与结果文件已开放；完整传输选项仍在P4。普通401与百度账号失效的422保持独立含义。

幂等重放保持首次逻辑结果、HTTP状态与资源版本；每次HTTP请求仍生成自己的request_id，并与X-Request-ID一致。


## 调整后的实施顺序

P3已收尾：40 implemented、3 partial、62 planned，完整范围与限制见P3指南/验收。UPL01、DWN01、JOB08继续保留P2受限路径；完整传输在P4。P3没有将辅助扫码或历史上游能力记为已实现。

planned/partial/implemented贯穿Capability、Catalog、路由和checker；supported_option_values表达规范字符串值，limits只放数值上限。credential_alternatives表达至少满足一组凭据，token-only不得据此取得Cookie能力。实际响应检查覆盖所有开放操作的成功路径和partial拒绝路径，并核对运行能力、SSE帧、HEAD/204与目标契约。

查询快照默认10000项/600秒、128个活跃快照/128MiB；扫描逐项改用不可变条目高水位游标，支持更大任务。事件每job10000个/86400秒、心跳15秒、128帧/连接及全局64连接。Artifact完成后TTL86400秒、单文件256MiB、总量1GiB/128文件；活动任务/响应引用阻止物理清理，过期读取410。详情及schema v4回滚说明见P3文档。
