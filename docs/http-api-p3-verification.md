# P3 实现与逐项验收

日期：2026-11-05。基线 `4fc00ce`，工作分支 `codex/http-api-design`。契约 `0.3.0-p3`；40 implemented、3 partial、62 planned。P3 的 24 个归属操作和 P2 的 4 个账号切片均已补齐；P2 传输范围保留，完整 UPL01/DWN01/JOB08 仍在 P4。

本轮离线验收通过：相关包race、P2四个SIGKILL窗口和传输回归、14个原CLI离线包、102组实际HTTP响应及完整契约、616条映射和13目标CGO=0构建。构建原始结果保存在[构建记录](http-api-p3-build-verification.json)，摘要见[机器记录](http-api-p3-verification.json)。本机服务运行范围为Linux/amd64，其他目标只验证编译。

本记录区分可重复离线验收和真实百度实测；不会将受控TLS计为真实百度。

同日使用用户新扫码会话完成专用目录真实验收，33个检查阶段通过，范围见[脱敏上游报告](http-api-p3-upstream-verification.json)。真实运行不替代分页规模、故障注入、TTL和慢消费者的离线证据。

## 逐项核销

| ID | 范围 | 验收证据 |
| --- | --- | --- |
| P3-01 | ACC01—ACC03：三种导入、独立 ID、后备显示名、脱敏、列表/读取和重放 | `TestP3AccountsCredentialsQuotaAndVersions`；保留 P2 凭据规范化与跨账号测试 |
| P3-02 | ACC04/ACC06：强 ETag、presence/null、规范化 Cookie Jar、独立 token、版本和吊销 | `TestP3CanonicalCredentialPatchPresenceAndRevocation`、`TestP3AccountsCredentialsQuotaAndVersions`；缺失 428、冲突 412、重复/混合/空值 400 |
| P3-03 | ACC05/ACC07—ACC09：删除、验证、配额、token-only 能力 | `TestP3AccountsCredentialsQuotaAndVersions`、P2 token-only 验证测试；未知身份为 null，Cookie 能力 disabled，不假报全局上游 available |
| P3-04 | 普通更新保留已提交版本，清除/删除撤销旧引用并关闭上游连接 | `TestP3ScanLimitsCancellationRevocationAndRestart`；旧加密凭据归档，撤销销毁秘密载荷；结果不明的传输仍 interrupted |
| P3-05 | 固定来源旧账号导入、重复 UID、只读、重启稳定 | `TestP3ExplicitLegacyAccountsImportIsStableAndReadOnly`、`TestP3LegacyImportRejectsConflictingCookieCopies`；不导入 CLI 当前账号/客户端参数，不重写源文件 |
| P3-06 | FIL01/02：分页、排序、投影、元数据和数值精度 | `TestP3FileReadingSnapshotsScansJobsAndArtifacts`：1001/2000/2001、空页、base_path、重复参数和大 fs_id；`TestP3CLISharedReadsPreserveAggregationOrderingAndOutput` 检查共享聚合解析 |
| P3-07 | FIL03/04/06：逐输入错误、搜索快照、查询/账号绑定、超限、通配符 | 同一 HTTP 测试；`TestP3SnapshotBindingExpirationAndEncryptedCursors` 覆盖损坏/过期/重开，10001 项 413 |
| P3-08 | FIL05：tree/recursive/statistics、深度/ID、资源截断、部分结果 | HTTP 扫描与 `TestP3ScanLimitsCancellationRevocationAndRestart`；完整成功、全部失败、部分成功、max_nodes、取消、重开后 retry |
| P3-09 | JOB01—JOB04：过滤、固定范围、逐项结果、终态和摘要 | HTTP 集成；`TestP3ItemPaginationHighWaterBeyondTenThousand`：10001项分页且后续新增项不混入旧游标；`TestP3JobQueriesApplyVisibilityBeforeLimitAndKeepTimePrecision` 验证权限先于上限和纳秒边界 |
| P3-10 | JOB06/JOB09：独立取消、重试新任务、scope、幂等和版本 | HTTP 集成和原 P2 任务/控制/恢复测试；`TestP3RetryScanPreparationFailureCreatesFreshJob` 覆盖尚无逐项记录的准备失败；新任务重新准备，原任务和检查点保留 |
| P3-11 | JOB05：持久事件、原子提交、重启序号、过期和重连 | `TestP3JobEventTransactionsAndRestartSequence`：创建/状态/检查点/取消/终态事件故障注入全部回滚；HTTP Last-Event-ID 正常回放、跨任务 400、未来 409；存储过期410，`TestP3EventCountRetentionNeverReusesSequence` 验证10000个保留上限 |
| P3-12 | SSE snapshot→跟随、终态发送、慢消费者和有界队列 | `TestP3SSESnapshotFollowsThroughTerminalCommit`、`TestP3SSESlowConsumerDisconnectsWithoutBlockingJob`、`TestP3SSEHeartbeatIsCommentWithoutID`；任务不受慢连接阻塞，断开后持久回放保留 |
| P3-13 | ART01—ART04/JOB11：元数据、Range、HEAD、TTL、引用、授权和清理 | HTTP 200/206/400/403/410/416；`TestP3ArtifactSingleRangeBoundaries`；`TestP3ArtifactReservationsEnforceAndReleaseTotalQuota` 验证预留/释放和总配额，`TestP3ArtifactTTLReferencesCleanupAndSummaryRetention` 检查活动任务/响应引用、到期、物理清理和摘要不丢失 |
| P3-14 | LOC01 以及受影响 CLI 读取 | HTTP root 权限/脱敏；`TestP3CLISharedReadsPreserveAggregationOrderingAndOutput` 检查 ls/search/meta/tree、排序、完整 ID、1001/2000/2001 聚合、重复页和中途错误 |
| P3-15 | P2 传输、取消、恢复和旧数据兼容 | 原受控 TLS、A/B/C 隔离、四个 HTTP SIGKILL 窗口、状态 CAS/迁移回归保留；`TestP3MigrationFromV3PreservesJobsAndCheckpoints`、`TestP3PreservesP2CheckpointBinding`、`TestP3PreservesP2IdempotentAdmissionIdentity`、`TestP3LegacyAdmissionReplaysInitialReceiptWithoutResettingJob` 专门核对兼容；`TestP3QueuedUnknownCommitSurvivesCancellationAndRevocation` 覆盖恢复排队窗口；v1/v2/v3→v4 增量迁移，不重建数据库 |
| P3-16 | 契约、能力、源码映射和构建范围 | 实际 HTTP JSON/SSE/二进制/HEAD/204 导出及 schema 检查；105 操作、89 路径、190 schema；原 CLI 目标矩阵不缩减 |

## 复现

Go 工具链 1.23.12；Python 依赖见 api/openapi/tools/requirements.txt。全部离线测试只连接受控本地 TLS；真实凭据测试默认 skip。

```sh
BAIDUPCS_P1_CONTRACT_RESPONSES=/tmp/baidupcs-p1-responses.json \
BAIDUPCS_P2_CONTRACT_RESPONSES=/tmp/baidupcs-p2-responses.json \
BAIDUPCS_P3_ACCOUNT_RESPONSES=/tmp/baidupcs-p3-accounts.json \
BAIDUPCS_P3_CONTRACT_RESPONSES=/tmp/baidupcs-p3-responses.json \
go test -race -count=1 -timeout=180s \
  ./internal/domain/... ./internal/application/... \
  ./internal/infrastructure/... ./internal/httpapi ./internal/bootstrap .

python api/openapi/tools/check_contract.py --live-responses \
  /tmp/baidupcs-p1-responses.json /tmp/baidupcs-p2-responses.json \
  /tmp/baidupcs-p3-accounts.json /tmp/baidupcs-p3-responses.json

# 范围与 P2 相同，新增 CLI 读取回归在 pcscommand 包内执行。
go test -count=1 -timeout=120s \
  ./pcsliner/args ./pcsutil/cachepool ./pcsutil/converter \
  ./pcsutil/escaper ./pcsutil/waitgroup ./pcsutil/taskframework \
  ./baidupcs/netdisksign ./baidupcs/expires/cachemap \
  ./requester ./requester/rio ./requester/rio/speeds \
  ./requester/uploader ./internal/pcscommand ./internal/pcsfunctions/pcsupload
python api/openapi/tools/check_builds.py --report /tmp/baidupcs-p3-builds.json
CGO_ENABLED=0 go build -o /tmp/baidupcs-http .
/tmp/baidupcs-http server --help
git diff --check
```

25组原行为规格中PAGE-01/PAGE-02/SSE-01/COOKIE-01/CAP-01已链接到实际离线测试，其他跨P4—P7规格继续待核销；链接检查本身不代替运行测试。新增的实际响应校验覆盖 SSE 事件 schema/帧 ID、文件内容以及所有 HEAD/204 空 body，不能只以合成示例代替 handler 运行。原有 downloader 编译测试、固定开发者路径和外网示例仍按 P0 基线分类，本阶段没有改动旧并行下载器。

## 持久化和恢复边界

schema v4 在 v3 上增加凭据历史/吊销、查询快照、逐项结果、artifact 索引和持久事件计数。旧账号凭据先兼容读取、首次更新时归档；旧 Record/Checkpoint 字段保持兼容，新增 Request 字段在空值时不参与编码，P2 的检查点绑定和幂等 job_id 算法保持。状态、时间和 version 仍以 SQL 列为准；事件清理不重用序号。

备份需同时保留数据库、master.key 和 artifacts；P2 二进制拒绝 schema v4。回滚须恢复升级前一致备份，不在已迁移数据库上直接降级。服务仍单实例、持久队列 2 执行/16 等待；有活动引用不清理结果文件。相关提交结果都检查错误，不能以超时或失败冒充已持久化成功。未新增主机掉电一致性承诺。

## 真实百度范围

P2 的真实凭据已按交接清理。本轮使用用户新扫码取得的Cookie会话，不读取CLI当前账号。真实测试于2026-11-05 07:44:23—07:44:53 UTC在Linux/amd64、Go1.23.12上通过，TLS证书校验开启，33个检查阶段全部通过。文件操作仅限`/BaiduPCS-Go-P2-Verification/`内本轮随机子目录，结束后子目录清理通过；不会列举日常根目录。

| 真实范围 | 结果 |
| --- | --- |
| ACC01—ACC09 | 导入、列表/读取、label/凭据更新、版本递增、验证、配额、能力、凭据清除和本地账号删除通过 |
| 文件读取与扫描 | P2受限单文件上传fixture后，单页列表、元数据、批量元数据、搜索、通配符、tree扫描和逐项结果通过 |
| 任务与部分成功 | 完整扫描succeeded；有效文件与缺失路径混合扫描partially_succeeded；仅重试失败项创建独立新任务并failed，保留预期的缺失路径失败 |
| SSE与结果文件 | snapshot、持久游标回放及终态事件；artifact元数据、单Range 206、HEAD、删除204和删除后410通过 |
| 清理与凭据复用 | 本轮远端子目录清理通过；临时服务数据由测试清理；用户授权的`.env`以0600保留并被Git忽略 |

原始阶段结果及参数见[脱敏上游报告](http-api-p3-upstream-verification.json)。单Cookie账号、单文件和小目录实测不扩写为token-only、多账号或1001/2000/2001项真实目录验证；这些行为、吊销并发、重启迁移、事件故障注入、慢消费者及24小时TTL/引用继续以本文件的离线测试分别核销。全局capability仍不因一次实测自动变为available。

已有 [P2 脱敏证据](http-api-p2-upstream-verification.json) 继续保留其原始账号/时间/操作范围，不扩写为 P3 的实测。


真实P3复验入口默认skip；可以显式提供私有凭据文件：

```sh
BAIDUPCS_P3_LIVE_CREDENTIAL_FILE=/absolute/private/credentials.json \
BAIDUPCS_P3_LIVE_REMOTE_DIR=/BaiduPCS-Go-P2-Verification \
BAIDUPCS_P3_LIVE_REPORT=/absolute/private/p3-sanitized-report.json \
go test -count=1 -timeout=600s ./internal/bootstrap -run '^TestP3UpstreamDedicatedAccount$'
```

已保存私有`.env`时可复用；不要打印其内容或启用shell tracing：

```sh
set -a
. ./.env
set +a
BAIDUPCS_P3_LIVE_REPORT=/tmp/baidupcs-p3-upstream-verification.json \
go test -count=1 -timeout=600s ./internal/bootstrap -run '^TestP3UpstreamDedicatedAccount$'
```

该测试只在批准目录内创建本轮子目录和上传fixture，然后读取/搜索/扫描，验证事件及artifact，最后删除本轮子目录。不会列举正常根目录。报告仅保存阶段、时间和平台，不含身份值、路径内容或秘密。
