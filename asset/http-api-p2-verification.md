# P2 交付与验证

工作区日期：2026-11-05；实际进程时间见[脱敏上游记录](http-api-p2-upstream-verification.json)。状态：**P2-01—P2-12 已收尾，可以继续 P3**。契约 `0.2.0-p2`，12 个 implemented、10 个 partial、83 个 planned。partial 是受限可调用子集，不能计为完整操作实现。目标仍为 105 个操作、89 个路径、190 个 schema、615 条源码映射。

2026-11-02 的内部组合试验是本阶段的前置证据；本次补齐生产 HTTP、bootstrap 装配、持久派发、权限、公共 DTO、控制幂等、关闭、迁移、故障窗口和实际百度验证。使用方式见[P2 指南](http-api-p2-guide.md)。

## 逐项核销

| ID | 交付与证据 |
| --- | --- |
| P2-01 | planned/partial/implemented 贯穿 YAML/JSON、Catalog、路由开关、Capabilities、例子和 checker。`supported_option_values` 表达固定值，limits 保留数值资源上限。`TestP2RuntimeAccountsCapabilitiesAndPersistence` 及实际响应 schema 校验覆盖开放状态和上限。 |
| P2-02 | ACC01/02/03/07 接入规范化凭据、加密导入、脱敏列表/读取、导入幂等、HTTPS 在线验证与元数据版本。复用原 PrepareUK 的 need_selfinfo=1 和 records 结构；UID/UK 区分、未知字段 null。`TestP2LiveHTTPTransfersAndRecovery`、`TestCredentialImportCanonicalization`、`TestP2TokenOnlyValidationCannotClaimCookieIdentity`、`TestP2RuntimeAccountsCapabilitiesAndPersistence` 及专用账号实测。 |
| P2-03 | bootstrap 装配唯一账号/传输服务，目录锁先于恢复和派发，ready 检查启用子系统；任务使用服务生命周期，提交连接断开后继续。`TestP2LiveHTTPTransfersAndRecovery`、`TestDurableAdmissionRepairsLostSubmissionAndRestart` 和既有 bootstrap 启动/目录锁测试。 |
| P2-04 | 显式账号、单个 local 源/绝对远端路径、真实已有父目录、固定选项和 1 B—32 MiB 限制；上传目录与下载目标正确追加 basename。显式 false/0 保留，不支持的选项 422。`TestP2HTTPRejectsUnsupportedAndMalformedRequests` 及真实上传/下载字节核对。 |
| P2-05 | queued 行是派发依据，启动及定期扫描修补入库后崩溃/断连窗口；提交/账号/控制分别记录幂等，控制回复与状态同事务。至少 24 小时，活动任务控制重放不提前过期；任务随记录保留。`TestDurableAdmissionRepairsLostSubmissionAndRestart`、`TestQueueBoundAndConcurrentSubmissionReplay`、`TestP2ControlReplayRetentionAndCiphertextFailure` 及 SIGKILL admitted 场景。 |
| P2-06 | JOB02/04/06/08 使用公共 DTO；记录不直接序列化。Job、Result、Progress、Problem、有效选项、nullable 字段和字符串整数通过实际 schema 校验。权限同时检查 owner、账号、root 和原传输 scope，未终结 result=409。`TestP2LiveHTTPTransfersAndRecovery`、`TestTargetLockCancellationAdmissionAndOwnerPermissions`。 |
| P2-07 | 两个执行名额、16 个等待名额，429/Retry-After；同远端目标或实际本地目标共用可取消锁，root 别名不能绕过。已确认完成可胜过取消；interrupted+committing/done 拒绝伪报取消，恢复先核验。`TestTargetLockCancellationAdmissionAndOwnerPermissions`、`TestInterruptedCommitCannotBeReportedCancelled`、`TestSingleFileTransferRoundTripAndCancellationIsolation`、`TestSingleFileTransferReconcilesLostCommitResponse`。 |
| P2-08 | 停接收、停止服务任务、等待检查点/终态后再关闭客户端、存储和锁；写入和关闭有期限，错误可见；超时仍不抢先关闭执行器的存储。`TestP2HTTPProcessSIGTERMInterruptsActiveTransfer`、原 bootstrap 生命周期测试与真实运行时重启验证。 |
| P2-09 | schema v1→v2→v3 增量迁移，状态/时间/version 以 SQL 为准；检查 RowsAffected，兼容旧记录中的 JSON null 检查点；新下载先登记随机临时文件分配，再创建文件。Unix 固定父目录描述符后打开和不覆盖提交；旧检查点转换与回滚边界见指南。`TestP2MigrationFromV1AndV2PreservesEncryptedState`、`TestP2StateWritesCheckAffectedRowsAndKeepVersions`、`TestP2ControlReplayRetentionAndCiphertextFailure`、`TestP2DownloadAllocationAndCommitFaultWindows`、`TestP2DownloadRootsAliasSameTargetAndCancelCleanup` 及实际恢复测试。 |
| P2-10 | Bearer→账号→上传→查询/结果→下载→取消→重开/恢复的真实 handler 响应；受控 TLS 使用生产百度协议、multipart 和执行器。`TestP2HTTPProcessSIGKILLRecoveryBoundaries` 在已入库未派发、已确认分片、上游已合并但响应未返回、done 已持久化但终态前四个点杀死真实子进程；确认分片和合并各只执行一次。错误 Range/长度、忽略 Range、提前断流、慢流取消和 body 空闲超时另有测试。 |
| P2-11 | 已使用用户明确授权的扫码会话及 `/BaiduPCS-Go-P2-Verification/` 内随机独立子目录实测。账号验证、1 MiB 上传及下载 SHA-256/字节一致、取消后后续任务、关闭/重启 interrupted→resume→succeeded、最终远端长度和清理通过。Linux/amd64，Go 1.23.12，app_id 266719；账号类型未查询。完整范围与时间见脱敏 JSON。 |
| P2-12 | 新增操作指南、可运行请求示例、实际能力约束、上游 opt-in 验收入口、迁移/保留/关闭说明，更新交接、设计、覆盖和 CI。P3/P4 完整业务仍独立核销。 |

## 离线与进程证据

```sh
export GOTOOLCHAIN=go1.23.12
BAIDUPCS_P1_CONTRACT_RESPONSES=/tmp/baidupcs-p1-responses.json \
BAIDUPCS_P2_CONTRACT_RESPONSES=/tmp/baidupcs-p2-responses.json \
go test -race -count=1 -timeout=180s \
  ./internal/domain/... ./internal/application/... \
  ./internal/infrastructure/... ./internal/httpapi ./internal/bootstrap .

python3 -m venv /tmp/baidupcs-http-venv
/tmp/baidupcs-http-venv/bin/python -m pip install -r api/openapi/tools/requirements.txt
/tmp/baidupcs-http-venv/bin/python api/openapi/tools/check_contract.py \
  --live-responses /tmp/baidupcs-p1-responses.json /tmp/baidupcs-p2-responses.json
python3 api/openapi/tools/check_builds.py --report /tmp/baidupcs-p2-builds.json
```

本轮相关包 race 通过；39 组实际 HTTP 响应覆盖全部 12 个 implemented 和 10 个 partial 的成功路径，以及所有 partial 的拒绝路径；67 个 schema 边界用例、105 组目标 HTTP exchanges、615 条映射校验通过。原 14 个明确离线的 CLI 包回归通过。13 个 CGO=0 CLI 目标编译通过，SQLite 仅在原五个服务目标链接。P0 已记录的三个旧测试问题保留，未运行不加筛选的全仓网络测试。

子进程测试通过存储边界注入“停在某次写入之后”的故障时机，使用生产 handler、应用服务、SQLite、ClientFactory 和 SingleFileEngine，网络连接本机验证证书的 TLS 百度协议服务。它们不计为真实百度传输；真实百度证据来自下节独立验收。目标机运行证据本轮只新增 Linux/amd64，不把交叉编译称作其他目标机运行通过。

## 真实上游复现

`TestP2UpstreamDedicatedAccount` 默认 skip，只有显式提供凭据文件和约定的测试根目录才运行。文件应采用 ACC01 的完整 JSON body，且为 0600 私有普通文件。测试不读取 CLI 当前账号，不列出正常目录，不打印凭据、账号标识、文件名或签名 URL；仅创建本轮随机目录和测试文件，最后删除该子目录。创建/清理是测试 fixture，FIL07 等 HTTP 路由仍 planned。

```sh
BAIDUPCS_P2_LIVE_CREDENTIAL_FILE=/private/account-import.json \
BAIDUPCS_P2_LIVE_REMOTE_DIR=/BaiduPCS-Go-P2-Verification \
BAIDUPCS_P2_LIVE_REPORT=/private/p2-live-report.json \
GOTOOLCHAIN=go1.23.12 go test -v -count=1 -timeout=10m \
  -run '^TestP2UpstreamDedicatedAccount$' ./internal/bootstrap
```

验收入口显式把运行环境的 HTTPS_PROXY 应用为本轮客户端配置；普通服务依然不隐式继承进程代理。扫码只是本次取得测试会话的独立辅助流程，不表示 LOG 登录会话或 qrlogin CLI 已实现。凭据输入文件由调用者管理。本轮扫码辅助流程创建的 Cookie、票据、二维码、凭据和原始临时目录均已清除；仓库仅保存脱敏结果，实值扫描无命中。

## 后续边界与余量

P3 仍需完整账号/凭据更新与吊销、账号迁移、文件读取与分页、任务列表/逐项结果/事件、重试、扫描与 artifact；P2 的持久派发和控制无需重写，任务/事件同事务仍在 P3。P4 仍需目录/空文件、完整策略、全部有效选项、并行预算、暂停、暂存、内容代理和旧下载器隔离。此轮只完成受限普通文件链路，不能把十个 partial 改成完整 implemented。

实时后台保留/磁盘配额、管理资源和主机掉电一致性没有提前承诺。P2 任务/检查点保留供恢复，有限名额和明确生命周期已经执行；原 35—52 工程工作日估算不由这次最小链路机械折减。后续应按 P3/P4 具体用例和上游证据重新评估，避免以 22 个可调用接口或全部 615 条映射代替全业务完成率。
