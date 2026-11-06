# P6 逐项验收记录

契约0.6.0-p6、SQLite schema v7，P6共30项；全契约104 implemented / 0 partial / 1 planned（P7 SYS07）。代码在codex/http-api-design worktree开发，83f6a83及所有P2—P5本地提交保留，未混入主检出的独立QR工作。

## 操作核销

T=TestP6IntegrityExportAndLocalCryptoHTTP，C=TestP6ContextsCLIAndReloadHTTP，L=TestP6LoginSessionsHTTP，U=TestP6ReceptionOffsetRecoveryAndHTTP，均在internal/bootstrap/p6_test.go。E为p6_edges_test.go的上下文冻结/重启密钥测试；F为localfs/p6_test.go的算法、故障、接收并发/配额测试。受控TLS上游不是百度实测。

| 操作 | 实现和受控验收 | 本轮真实范围 |
| --- | --- | --- |
| INT01 | T；长度、完整/分片MD5、CRC32、缺头null、逐项失败与可信度 | 批准目录内新文件通过 |
| INT02 | T；修复job、缺摘要逐项失败、未知提交不重发 | 修复请求被百度明确拒绝；成功修复仅受控验收 |
| INT03 | T/F；受限本地长度、MD5、slice、CRC32、4MiB块MD5 | 本地能力，无远端步骤 |
| INT04 | T；远端和受限本地文件逐算法校验、缺摘要/不一致 | 新文件size/md5通过 |
| INT05 | T；服务link_ref、owner/account/凭据绑定、完整md5/sha256校验及逐跳目标限制 | 同轮签发direct引用的sha256通过 |
| INT06 | T；合法输入503、UPSTREAM_CAPABILITY_UNAVAILABLE、retryable=false | 按长期不可用契约，未创建假job |
| EXP01 | T；三种格式、递归、虚拟根改写、部分成功、artifact及受限本地输出 | 新文件jsonl导出成功；递归/旧格式为受控证据 |
| FIL12 | T；大整数ID、上游cursor/has_more/reset，错误不伪造增量 | 受控；未读取账号整体增量 |
| LOC02 | T/F/E；9种兼容AES、gzip、原子不覆盖、源默认保留、临时秘密与重启 | 本地能力 |
| LOC03 | T/F/E；旧字节格式解密、错误密钥/损坏gzip、源删除条件、失败清理 | 本地能力；raw无认证格式限制见指南 |
| LOG01 | L；password/qrcode独立会话，严格输入、幂等创建 | 受控；复用现有Cookie验证其他业务，无新登录 |
| LOG02 | L；只推进保存的轮询状态、owner隔离、TTL | 受控 |
| LOG03 | L；受权PNG、二维码/captcha图片，不公开上游签名 | 受控 |
| LOG04 | L；captcha、短信/邮箱发送与验证分支、挑战ID绑定、未知挑战拒绝 | 受控；没有真实密码、短信或扫码确认步骤 |
| LOG05 | L；写入前验证UK、原始权限、禁止客户端临时BDUSS、重复complete同账号 | 受控 |
| LOG06 | L；撤销后不能complete，销毁交换状态 | 受控 |
| CTX01 | C/E；账号和目录校验、owner、TTL及强ETag | 专用目录创建成功 |
| CTX02 | C/E；账号allowlist、所有权、过期410 | 通过 |
| CTX03 | C/E；强If-Match、切换/相对cwd、listing，冻结所有受影响业务选择 | 专用目录更新/listing通过 |
| CTX04 | C；删除与后续410 | 通过 |
| CFG08 | C；固定受控CLI快照的独立account_id/默认cwd映射 | 受控配置；未改日常CLI配置 |
| CFG09 | C；CAS、原子写回、账户/目录校验、幂等回执；与context/job隔离 | 受控配置 |
| CFG06 | C；固定来源、dry-run、版本比较、验证失败不提交、启动/权限边界保护 | 受控配置 |
| SYS05 | C；admin、无主动探测默认、固定HTTPS目标、5秒期限及脱敏输出 | 受控网络探测 |
| SYS06 | C；实例候选节点、域名校验、未确认有效时间为null、无节点明确不可用 | 既有Cookie查询通过 |
| UPL05 | U/F；创建/幂等、长度/摘要、与P4共享配额、独立文件ID | 本地接收能力 |
| UPL06 | U；偏移/状态/TTL、重开修复、完成前ID为null | 本地接收能力 |
| UPL07 | U/F；Upload-Offset、32MiB、CAS并发、fsync后提交、断连/磁盘满回退 | 受控；含实际进程SIGKILL |
| UPL08 | U/F；完整长度/摘要、零字节、重复同ID、完成窗口恢复 | 本地接收能力 |
| UPL09 | U/F；取消、过期清理、引用保护及配额释放 | 本地接收能力 |

## 故障、持久化和边界

TestP6CryptoModesLegacyCompatibilityAndFailureCleanup验证9种算法、gzip的两种取值，以及原bdcrypto产生的字节流。错误密钥/损坏gzip不发布明文；原始无gzip流没有认证标签，未假报可以验证所有错误密钥。TestP6CryptoDiskFullCancellationAndCommitReceipt注入ENOSPC、取消及最终回执丢失，默认源保持，未知结果明确保留。

TestP6ToolsRejectSymlinkAndFIFO验证受限工具不打开逃逸符号链接或FIFO。TestP6RestartLosesKeyExplicitlyAndCleansUncommittedPlaintext验证重启缺密钥、任务明确不可恢复、临时明文清理和源保留。密钥不进入公共job、事件、日志、artifact或数据库；检查点只包含受限临时引用。

TestP6ContextFreezesSelectionAndIdempotentReplay覆盖两个账号、跨账号context、fs_id选择不能绕过校验、context/base_path互斥、强ETag及已接受任务的冻结/重放。登录通过两个owner、独立Cookie Jar和保存的挑战状态验收；TestP6LoginTTLAndUnknownChallengeFailClosed验证TTL销毁和未知挑战拒绝。

TestP6ReceptionCASQuotaDigestDisconnectExpiryAndDiskFull验证并发同偏移只能一个成功、断连和磁盘满不推进偏移、摘要不符不发布、TTL清理。TestP6ReceptionQuotaReservesIncompleteData证明未完成接收占用共享配额且取消释放。TestP6ReceptionSIGKILLHTTP启动真实HTTP子进程，在块已写文件而DB偏移未提交时SIGKILL，重启后offset仍0，再接收完整内容且字节一致。文件fsync后/DB前的长尾恢复另由U测试模拟；没有将进程SIGKILL扩大为真实物理断电测试。

v1—v5既有迁移、检查点、控制回执和暂存引用测试升级目标为v7；v6→v7增量表结构保留旧数据。更高版本库继续拒绝旧二进制，不能通过降低版本号回滚。停机一致备份/恢复见P7工具与指南。

## 真实百度证据

TestP6UpstreamDedicatedAccount复用被忽略0600 .env中的既有Cookie，TLS校验开启，所有文件写入仅在/BaiduPCS-Go-P2-Verification/本轮独立子目录。现有凭据验证、context上传、INT01/04/05、EXP01、CTX01—04、SYS06及清理通过，详见http-api-p6-upstream-verification.json。LOG01—06、FIL12、多账号、超大文件和故障注入不在真实范围。首次运行仅报告文件使用了包内相对路径导致保存失败；改为绝对报告路径后重跑成功，未扩大远端授权范围。

完整race、实际响应契约、14包CLI回归及13目标CGO=0构建的最终执行结果写入http-api-p6-verification.json与构建报告。本轮实际运行平台仅linux/amd64；构建不能代替其他平台运行。

修复协议补充：原核心RapidUploadNoCheckDir会忽略XPan errno；P6改为同时核对errno/error_code，缺状态不算成功，明确拒绝不伪报成功。修正前的真实INT02成功结论已撤回，最终报告记录为upstream_rejected；中间只读网络超时也保留在prior_attempts。

最终执行结果：完整相关包race通过；最后受影响P6/迁移/core race通过，216组实际HTTP响应覆盖全部104个implemented操作，契约检查不带--write-inventory复验通过。14个历史CLI离线包和13目标CGO=0构建通过。构建后的可移植解析/秘密指纹/会话修正另有native编译及race记录，不重复不受影响的交叉矩阵。详情见JSON报告。
