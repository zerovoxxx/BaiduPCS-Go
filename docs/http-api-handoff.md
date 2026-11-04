# HTTP API 工程交接：P2 已交付，从 P3 继续

日期：2026-11-05。分支 `codex/http-api-design`。P0/P1 历史基线保留；当前契约 `0.2.0-p2`。本文件描述本次工作树的最终交付，原业务源码基线仍为 `1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73`。

## 1 当前完成到哪里

| 范围 | 当前事实 | 仍需区分 |
| --- | --- | --- |
| P0 | 105 个操作、89 个路径、190 个 schema；当前 615 条源码映射 | 目标契约覆盖不等于业务实现率；25 组完整行为规格仍按后续阶段核销 |
| P1 | 配置快照、隔离客户端、Bearer、SQLite、生命周期与 12 个系统/配置操作 implemented | 保留全部成果及 CLI 构建范围 |
| P2 | 账号/受限单文件/任务 HTTP、持久派发、取消、迁移、关闭及恢复已接入生产；离线/race、四个 SIGKILL 窗口和真实百度验证通过 | 10 个新开放操作是 partial；完整账号和任务仍在 P3，完整传输在 P4 |
| QR | 本轮扫码仅用于显式授权的独立上游验收 | qrlogin CLI 和 HTTP 登录会话仍 planned；不将辅助扫码流程计为 LOG 操作实现 |

当前状态为 **12 implemented、10 partial、83 planned**。partial：ACC01/02/03/07、UPL01、DWN01、JOB02/04/06/08。planned 操作通过鉴权后仍返回 501。P2-01—P2-12 的逐项证据见[P2 验证记录](http-api-p2-verification.md)，真实范围见[脱敏记录](http-api-p2-upstream-verification.json)。

推荐顺序：本文件 → [P2 操作指南](http-api-p2-guide.md) → [P2 验证记录](http-api-p2-verification.md) → [实施计划](http-api-implementation-plan.md) → [设计](http-api-design.md) → [契约校验](../api/openapi/README.md)。逐项业务语义查[源码覆盖矩阵](http-api-coverage.md)。

## 2 代码接入点与后续缺口

| 位置 | P2 已有能力 | P3/P4 继续的工作 |
| --- | --- | --- |
| internal/bootstrap/server.go | 独占目录锁后装配账号/唯一传输服务；恢复、ready、HTTP、任务和存储有序关闭 | 沿现有生命周期接事件、读取及其他引擎，不再重建运行基础 |
| internal/httpapi/p2.go、jobs.go | 十个 partial handler、严格输入、脱敏 DTO、权限、字符串整数、固定有效选项及控制响应 | 分页、完整任务/结果模型、SSE 和全部选项；避免直接公开 Record/Entry |
| internal/application/accounts/service.go | 三种凭据规范化、独立账号 ID、导入重放、列表/读取、在线校验和元数据 revision | 凭据更新/吊销、旧配置导入、完整身份字段和交互登录；保留 presence/null 语义 |
| internal/application/transfers/service.go | 持久 queued 扫描、2 执行/16 等待、幂等提交/控制、可取消目标锁、有界关闭 | 任务/事件同事务、列表/逐项结果、重试与完整清理配额；不引入通用工作流 |
| internal/infrastructure/persistence | schema v3 增量迁移、AES-GCM、状态/时间/version CAS、控制回复事务、活动重放保留 | 在 v3 上新增迁移与索引，不能重建用户 DB 代替迁移 |
| internal/infrastructure/baidu | 实例网络/凭据绑定；PrepareUK 原请求构造；HTTPS 单文件传输、指纹/分片/提交结果核验 | 完整握手、目录、策略、上传 ID 失效、限速与并发；不复用旧全局上传客户端 |
| internal/infrastructure/localfs | 真实目标键、Unix 目录描述符打开与不覆盖提交；只清理确认取消任务的随机临时文件 | 与完整目录、暂存、配额及各平台运行一起扩展；旧无记录文件不猜测归属 |
| requester/downloader/context.go | 独立单连接、256 KiB 持久确认单位、严格 Range；请求空闲/取消由绑定客户端控制 | 旧 Execute/monitor/全局 cache 隔离仍在 P4，不把新增路径当作旧并行路径已完成 |
| internal/bootstrap/live_test.go | 显式凭据文件/专用目录的 opt-in 真实测试；正常离线 CI 默认 skip | 对新增业务按账号能力追加明确实测，不使用日常文件、不输出凭据或目录内容 |

`transfer.Record` 包括代理和私有检查点/uploadid，只能加密持久化，不能作为 HTTP 数据。SQL 状态与版本是权威字段。账号 DTO 的未知 UID/UK、性别/年龄等必须为 null，不能以 UK 冒充 UID。

## 3 已落实的接续决策

1. 保留同 module、标准库 HTTP、模块化单体、SQLite 和五层边界。每个新 port 必须有当前使用者；不增加外部队列、微服务或空管理框架。
2. planned/partial/implemented 已贯穿契约、Catalog、路由、能力和校验器。partial 能执行但不算完整实现；`supported_option_values` 给规范字符串值，limits 只放数值上限；`x-phase` 仍是完整验收归属。
3. P2 使用显式账号、单个 local 源/绝对远端文件、已有授权 root、固定选项及轮询。目录/空文件/暂存/其他参数明确拒绝，P3/P4 按完整契约补齐。
4. 配置仍可保存 enable_https=false，但携带上游凭据的客户端明确拒绝创建；绑定 Transport 和重定向禁止 HTTP 降级，Cookie 是 Secure。原 HTTPS/HTTP Cookie 查询已按 PCS 协议校正；未使用 UID 的旧明文身份回退。
5. 原始 Cookie 文本模型不等于结构化会话。STOKEN 仅安装在 Pan，PTOKEN 不广播到其他子域；P6 扩展 QR 会话仍需保存 domain/path/Secure/expires 等作用域，不合并 Passport 和 Pan 同名 Cookie。
6. 取消不等于回滚。未知提交结果保留 interrupted；committing/done 的中断任务先 resume 核验，不能伪报 cancelled；已知 cancelled 不自动复活。配置和凭据版本绑定已入库任务，后续凭据更新要定义明确恢复规则。
7. 本轮专用账号实测不把所有账号/所有操作标为 available。运行中百度业务仍 unverified、verified_at=null，需要按账号、平台、参数和时间单独认定；受控 TLS 与真实百度证据分别记录。

## 4 P2 交付核销

P2-01—P2-12 全部有实现或明确实测结论，逐项测试名与复现见[P2 验证记录](http-api-p2-verification.md#逐项核销)。交接者可以从 P3 继续。

当前受限值：上传 file_concurrency=1、parallel_per_file=1、max_retries=0、policy=skip、skip_rapid_check=true、check_filename=false、no_split_file=false；下载 mode=pcs、path_mode=relative、parallel=1、file_concurrency=1、max_retries=0、overwrite=false、verify_checksum=true，其余布尔 false、link_index=0；两种速率均为字符串 "0"。省略字段继续按契约继承配置，不静默改成固定值；超出受限能力返回 422。上传 skip 正确计 skipped/0 字节。

P3/P4 的全量契约验收不因 P2 提前切片而完成。若新增能力超出现有 fixture，应先补对应测试，再演进 partial/implemented 声明。

## 5 P3—P7：完整验收归属与不可遗漏项

以下按当前 OpenAPI 的 `x-phase` 核对，共 105 个操作；P2 partial 提前切片不改写完整验收归属。

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
- 当前目录锁在 bootstrap；transfers.New 会把遗留 running/cancelling 记录核验中断、保留 queued 派发，所以只能由获得锁的唯一实例调用。测试直接组合 Store/Service 不等于允许生产多实例。
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
BAIDUPCS_P2_CONTRACT_RESPONSES=/tmp/baidupcs-p2-responses.json \
go test -race -count=1 -timeout=180s \
  ./internal/domain/... ./internal/application/... \
  ./internal/infrastructure/... ./internal/httpapi ./internal/bootstrap .

/tmp/baidupcs-http-api-venv/bin/python api/openapi/tools/check_contract.py \
  --live-responses /tmp/baidupcs-http-responses.json /tmp/baidupcs-p2-responses.json

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

本轮结果：相关包/race 与 14 个既有离线包通过；39 组实际 HTTP 响应覆盖 12 个 implemented 和 10 个 partial；105 个目标操作、190 个 schema、67 个边界例、615 条映射通过；13 个 CLI 目标 CGO=0 构建通过，SQLite 仅在 5 个服务目标导入。真实百度、Linux/amd64 的范围另见脱敏报告。

默认不要直接把 `go test ./...` 当作验收命令。已知原基线问题是 downloader 测试包 `ShowWorkers` 编译失败、pcsutil.TestWalkDir 和 checksum.TestChecksum 的开发者绝对路径；getip 和旧下载示例会访问外网/写文件。详情见[P0基线](http-api-p0-baseline.md)。P4 改动旧下载器时先修正其测试入口与 fixture，不能因为包当前不可测试而跳过新逻辑验证。

契约变更流程：修改 YAML/设计/示例 → `python3 api/openapi/tools/export_contract.py` → 实际 HTTP 测试重新导出响应 → checker。源码或覆盖行变化时，先审人工矩阵，再运行 checker 的 `--write-inventory`，最后不带该参数检查；不能用重建清单掩盖未分类项。现有 CI 在 `.github/workflows/http-api.yml`，根 README/发布说明须在对外功能变化时更新。

## 8 交接验收方式

后续变更从 P3 完整账号/读取及任务完善开始，列出具体 operationId、支持的请求、仍不支持字段和运行证据。P2 逐项交付证据保留，不重写历史快照。每次只把有证据的项改为完成。P3—P7 按操作表和覆盖矩阵逐项核销，发现上游不可用则记录限制与证据，不补造成功响应。所有真实账号/协议实测均记录时间与范围；任何后续工程师不需要依赖这段聊天或本机 `/tmp` 文件才能判断还有什么没做。
