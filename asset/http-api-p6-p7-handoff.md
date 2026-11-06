# BaiduPCS-Go HTTP API：P6/P7 新对话执行交接

这是用户授权的完整开发接续指令。用户要求在同一个 BaiduPCS-Go 项目下另起持久对话，将剩下的 P6 和 P7 全部开发工作交给该对话。接收后先核对基线和文档，直接从 P6 开始，完成后继续 P7，持续实现、逐项验收、提交并报告。不要仅给出计划、占位路由或等待用户重复授权。

## 项目、工作目录与基线

- 项目主目录：`/workspace/github/BaiduPCS-Go`，新对话应归属该项目。
- **实际业务开发目录：`/workspace/github/BaiduPCS-Go-worktrees/codex/http-api-design`**，后续业务代码、Git操作和测试均在这里执行。
- 分支：`codex/http-api-design`。
- 当前业务基线：`4c86307`；P5完整实现 `79ec471`，核心修复 `1cf0e91`，P4交付 `f5976ad`，P3提交 `d1ae687` / `cd75eb4`。P2远端旧基线为 `4fc00ce`。这些本地提交必须全部保留，不能重置到远端旧基线。
- 本交接文件以独立文档提交接在 `4c86307` 后；启动时HEAD可能是该文档提交，核验祖先关系后继续即可。
- 主检出还有独立QR登录工作，不要在主检出开发，不要将该独立工作整体混入本分支。可只读参考相关协议，HTTP登录仍须在本分支独立服务化并验收。

先检查实际工作目录及祖先中的AGENTS.md，存在则遵循。必须阅读：

1. `asset/http-api-handoff.md`
2. `asset/http-api-implementation-plan.md`
3. `asset/http-api-design.md`
4. `asset/http-api-coverage.md`
5. `asset/http-api-p3-guide.md`、`asset/http-api-p3-verification.md`
6. `asset/http-api-p4-guide.md`、`asset/http-api-p4-verification.md`
7. `asset/http-api-p5-guide.md`、`asset/http-api-p5-verification.md`、`asset/http-api-p5-verification.json`
8. P2—P5各阶段脱敏上游报告及P4/P5构建报告；P2指南/验收中的原检查点兼容与HTTP SIGKILL入口。
9. `api/openapi/README.md`、`api/openapi/openapi.yaml`、`.github/workflows/http-api.yml`

## 当前事实与已固定边界

契约 `0.5.0-p5`，SQLite schema v6；105个operation、89条路径、191个schema，**74 implemented / 0 partial / 31 planned**，622条AST源码映射。P3账号/读取/任务/事件/artifact，P4十个核心传输操作及独立DWN05票据，P5全部23项均已交付。

相关包race、151组实际HTTP契约响应、14个历史CLI离线包、核心baidupcs回归以及13个CGO=0目标构建通过。最后增加的UK>0身份校验及核验后下载子任务只选成功项也已完成受影响P5 race。构建不等于其他目标机运行；本轮实际运行仅linux/amd64。

P3真实上游33阶段、P4真实上游39阶段通过。P5真实文件写入、新分享生命周期和分享检查通过；同账号转存被百度明确拒绝，成功转存/下载子任务、离线、回收站和故障窗口是受控上游证据。不要把受控fixture计作真实百度成功，也不要把历史报告扩大到新参数/账号类型。

必须保留：

- 同module、标准库HTTP、五层边界、单实例SQLite与目录锁、2执行/16等待的有界派发；复用现有传输、扫描、写业务和持久任务能力，不重建通用任务平台。
- HTTP不借用pcsconfig.Config、pcscommand全局当前账号或全局停止/信号状态。
- 普通凭据更新仍绑定旧版本；null清除撤销；未知远端提交仍interrupted，不能假报failed/cancelled、回滚或自动重发。JOB10只读核验，换凭据需原计划和当前有效百度UK一致，UK=0不能授权核验。
- retry新建任务、重新准备，不复制原uploadid/下载临时文件/检查点。已有P2请求空新增字段的序列化、幂等job_id和检查点绑定保持兼容。
- 持久事件序号/终态、SSE重连、逐项高水位游标、artifacts TTL/引用/原权限，以及staging对paused/interrupted的引用保护保持。
- P5分享/文件ID选择、写入逐项部分成功、同步SHR01提交意图、未知结果禁止重放；核验后的下载子任务只包含已确认成功的转存项。
- NoSplitFile、Rname、SuperTransfer保持实际支持或明确错误，不能因历史声明而假报成功。

## P6：30项全部核销

操作ID：**CFG06、CFG08、CFG09、CTX01、CTX02、CTX03、CTX04、EXP01、FIL12、INT01、INT02、INT03、INT04、INT05、INT06、LOC02、LOC03、LOG01、LOG02、LOG03、LOG04、LOG05、LOG06、SYS05、SYS06、UPL05、UPL06、UPL07、UPL08、UPL09**。

先补齐摘要/修复/导出/增量、本地加解密和完整HTTP登录，再补上下文与后置管理；浏览器续传接收也属于本次剩余工作，不能因其扩展属性而跳过。

### 摘要、校验、导出、增量

- INT01—INT05：远端长度、MD5、256KiB slice MD5、CRC32、MD5修复job、本地摘要job、远端与受限本地文件校验job，以及本服务签发link_ref的摘要读取。复用当前主项目有效协议；缺头、摘要可信度、超时、部分失败必须有明确结果，不能编造0或错误摘要。
- INT05禁止任意URL抓取或任意代理；校验link_ref的owner/account、有效期及逐跳目标，沿现有内容服务边界。
- INT06：旧式摘要秒传明确按现有契约返回上游长期不可用（合法输入503、retryable=false等），不能伪造成功job。此类明确实现的不可用语义与planned占位不同，能力availability必须真实。
- EXP01：支持约定的recursive、root_path改写、jsonl/legacy_command/legacy_link格式、max_retries、逐项部分成功和artifact；流式有界，不将凭据、密钥或内部签名状态写入导出文件。
- FIL12：保留上游cursor、has_more、reset_required及真实不可用状态。失败不吞错、不虚构增量结果或全量游标。

### 本地加解密

- LOC02/03：受限root/local引用、兼容算法、gzip选项、只写密钥、原子输出和取消/失败清理。
- 默认保留源，只有结果提交成功且remove_source=true才删除源。密钥不能进入公共job、事件、日志或artifact；按设计处理临时秘密，重启后缺失密钥须明确不可恢复，不能用空密钥继续。
- 验证错误密钥、损坏输入、磁盘不足、冲突、不覆盖、符号链接/FIFO逃逸、取消和重启边界。

### HTTP登录会话

- LOG01—LOG06：完整实现password/qrcode会话创建、查询、二维码图片、已实现challenge响应、complete建立/更新账号、撤销和TTL清理。
- 保留原password/challenge流程及机器状态；未知challenge明确拒绝。LOG05只能使用服务保存并核验的交换状态，不能接受客户端临时BDUSS绕过登录。
- 会话/二维码/挑战/交换令牌绑定owner、方法与原始权限，秘密不进DTO/日志；并发会话相互隔离，重复complete幂等，取消/过期后不能完成，凭据写入沿现有账号服务。
- 主检出的辅助扫码不计为这六个HTTP操作已经实现；不能仅迁入二维码辅助脚本后宣告完整登录。
- 真实验证复用现有有效Cookie，**不要为测试新登录接口要求用户重复扫码**。密码、短信、扫码确认等缺少可自动完成授权的实际登录步骤，用受控上游验收并明确真实范围；仅原凭据失效时联系用户。

### 上下文与管理/诊断

- CTX01—CTX04：owner、account、cwd、强ETag/If-Match、切换账号/目录、include_listing和删除；账号allowlist与目录校验不能省略。
- 把context路径解析接入所有受影响的现有文件、上传、下载、分享、转存、离线等业务入口；不能只添加四个CRUD路由而保留旧CONTEXT_NOT_AVAILABLE路径。
- context与base_path互斥，不同账号context返回明确冲突。提交时冻结账号、路径和有效选项；后续context变化不影响已有任务。两个调用方/上下文并发不串用。
- CFG08/09：CLI默认账号和各账号默认cwd的兼容映射，和HTTP context、运行job隔离；不共享可写pcsconfig全局对象。使用受控测试配置，不改用户日常CLI配置。
- CFG06：仅从固定授权来源重载，支持dry_run及版本比较；验证/提交失败不改当前配置，不允许请求选择任意文件，不借重载扩大身份/根目录/网络权限边界。
- SYS05：受权网络诊断、可选主动出口检查，脱敏代理/本地敏感信息、严格超时和目标限制。SYS06：实例PCS候选节点、有效时间和真实不可用状态，不能返回内部凭据/签名。

### 浏览器续传接收扩展（必须交付）

- UPL05—UPL09：创建/查询会话、Upload-Offset二进制PATCH、complete校验并发布staged_file_id、取消清理。延续现有暂存大小/总量/数量/TTL和owner/引用边界。
- 接收断点与向百度上传断点独立；偏移冲突409、持久化成功后才前移offset、未完成不发布staged_file_id、重复complete返回同ID。
- 并发PATCH/complete必须串行或CAS，掉电/进程SIGKILL后的文件长度/DB偏移可恢复，不能覆盖其他会话文件。长度、MD5等约定摘要、磁盘不足、断连、过期、零字节及完成后引用/清理均验收。

## P7：SYS07和全量交付收尾

- SYS07 `/metrics`：按契约授权，提供队列、任务、错误和传输等有界指标；label不包含job/account/owner ID、路径、URL、Cookie、密钥或任意上游字符串，避免秘密泄露和无界基数。抓取不能阻塞执行或扫描全量历史任务。
- 汇总P2—P6证据，逐项核销全部105个operation及CLI/配置/选项映射；源码映射率、实现率、离线验收和真实上游范围分别报告。
- 验收有界队列、多账号/上下文/会话并发、取消/暂停/恢复、未知结果、凭据换版/撤销、断连/慢网络、目录大数据、磁盘不足、资源配额、服务关闭和重启/SIGKILL窗口。
- 在schema v6上必要时增量迁移，验v1—v6旧数据/检查点/控制回执、staging引用兼容；停机一致备份master.key、DB及其日志、artifacts/staging/新接收数据，提供可执行恢复/回滚说明。旧二进制拒绝高版本，不能降版本号代替恢复。
- 同步YAML、内嵌JSON、示例/行为规格、capabilities的实际支持选项、设计、源码覆盖/清单、测试、CI、指南、发布说明和交接。
- 最终目标31个剩余操作全部完成，planned/partial归零；历史上游不可用按明确契约和真实capability表达，不把未实现核心业务包装成“不可用”或扩展而跳过。
- 按合理单元提交，先P6后P7，完成后逐operation报告实现、受控测试、真实上游状态、提交及边界。不要自动push/部署或触及无关服务。

## 工具链、回归和真实授权

- Go：`/tmp/baidupcs-toolchain/bin/go`（Go1.23.12）；默认PATH无go时先加入该bin目录。
- Python契约环境：`/tmp/baidupcs-p2-venv/bin/python`；临时环境缺失按`api/openapi/tools/requirements.txt`重建。
- 受影响检查随开发运行；阶段收尾运行完整契约、相关race、14个CLI离线包、核心回归和13目标CGO=0构建。完整race用`-p=1 -timeout=600s`，避免和全量交叉编译并发造成SQLite fsync干扰；变更未影响构建结论时不重复全量交叉编译。
- 现有CI `.github/workflows/http-api.yml`导出九组实际HTTP响应；新增P6/P7响应覆盖所有开放操作，再运行`check_contract.py`。更新矩阵先人工审，再`--write-inventory`，最后不带该参数检查。
- 原downloader测试编译失败、固定开发者路径、外网示例按P0基线单列，不能用无筛选`go test ./...`代替明确验收范围。
- 用户继续授权读取实际worktree根目录的`.env`作真实验证：文件0600、Git忽略且未跟踪，包含`BAIDUPCS_P3_CREDENTIALS_JSON`、`BAIDUPCS_P3_COOKIE_JAR_JSON`和批准目录变量。不要打印、提交或在报告中泄露任何值，不启用shell tracing，不把完整跨域Jar直接拼成Cookie头。
- 真实远端文件操作**仅限 `/BaiduPCS-Go-P2-Verification/`**，每轮独立子目录，结束清理本轮资源并保留`.env`。分享/离线/回收站真实操作也只触及本轮资源；不得清空全局回收站、全局离线列表或影响已有分享/日常文件。无法限定范围的用例用受控上游，明确实测边界。
- 凭据有效时不重复要求扫码；仅失效时联系用户。任何需要真实密码/短信/扫码确认但未自动获准的步骤不伪造完成，用受控证据说明范围。

接手后先确认基线和交接文件，直接开始P6开发，持续推进到P7完成。已有授权与约束随本指令移交，不需要用户重复发送背景或逐步确认。
