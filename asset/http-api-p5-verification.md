# P5 网盘写业务验收记录

日期2026-11-05。P4通过后继续P5；基线d1ae687与全部P3变更保留，P4提交f5976ad，P5首个修复提交1cf0e91，完整实现提交79ec471。契约0.5.0-p5、SQLite v6；74 implemented / 0 partial / 31 planned。操作实现、受控上游、真实百度与构建分别统计。使用与限制见[P5指南](http-api-p5-guide.md)，P4的11项独立列于[P4验收](http-api-p4-verification.md)。

## 23项逐项核销

下列操作均已实现并通过生产handler+受控TLS百度协议验收。F=TestP5FileWritesPartialConflictsAndRetry，S=TestP5SharesInspectionPaginationCollectAndDownloadChild，O=TestP5OfflineWindowIDsAndRecycleActualRestoreIDs，R=TestP5UnknownWriteReconciliationRestartAndNewCredentials，均位于internal/bootstrap/p5_test.go；附加边界测试位于p5_edges_test.go。离线协议fixture不计作真实百度成功。

| 操作 | 实现 / 受控验收结论 | 本轮真实百度状态 |
| --- | --- | --- |
| FIL07 | implemented；F：批量目录、已存在目录、空目录 | 通过，批准目录内新建 |
| FIL08 | implemented；F：路径/ID/模式、ignore_missing、部分成功、逐项ID；幂等重放已删除模式 | 通过，批准目录内删除/忽略缺失 |
| FIL09 | implemented；F：fail/skip/overwrite、部分成功、只重试失败项；核心prepare错误回归 | 通过，复制/skip/不同内容overwrite及目标大小核验 |
| FIL10 | implemented；F：移动、冲突选项、源身份校验 | 通过 |
| FIL11 | implemented；F：同目录重命名、拒绝跨目录 | 通过 |
| SHR01 | implemented；S：37天/密码/combined；已知失败回执及未知提交不重放 | 通过，本轮新分享3天/密码/combined |
| SHR02 | implemented；S：真实主项目页码、按需详情补密码、不伪造total | 受控通过；未读取账号既有分享清单 |
| SHR03 | implemented；S：仅返回可确认字段，其他null | 本轮新分享详情通过 |
| SHR04 | implemented；S：批量job、真实share_id | 本轮新分享取消/清理通过 |
| TRF01 | implemented；S：206根项目、目录展开、游标、所有权/凭据版本；整段文本和提取码优先级 | 通过；兼容上游字符串fs_id，保持大整数精度 |
| TRF02 | implemented；S：首批以外ID、嵌套ID、collect、可见性校验、下载子任务；拒绝无关ID/Rname | 请求执行到百度后明确拒绝SHARE_TRANSFER_REJECTED；同账号样本未获得成功，成功链路仅受控验收 |
| OFF01 | implemented；O/R：URL仅按CloudDl协议提交、实际task_id、未知ID关联；源HTTP服务器收到0请求 | 受控通过；未创建真实账号离线任务 |
| OFF02 | implemented；O及1001项边界：只取真实1000项窗口、本地cursor和complete_history=false | 受控通过；未读取账号既有任务 |
| OFF03 | implemented；O：逐ID结果/缺失；101个413且上游调用数不变；core同样拒绝 | 受控通过 |
| OFF04 | implemented；O：取消job和上游任务ID分离 | 受控通过 |
| OFF05 | implemented；O：指定ID删除job | 受控通过 |
| OFF06 | implemented；O：显式全局清空job；write/账号授权，不隐式触发 | 受控通过；禁止本轮真实全局清空 |
| OFF07 | implemented；O：读取单个真实上游ID，缺失404 | 受控通过 |
| REC01 | implemented；O：页码100，upstream_left_time=-17原值，时间null | 受控通过；未读取既有回收站 |
| REC02 | implemented；O：输入ID与实际恢复返回ID不同，保存实际大整数ID | 受控通过；未触及真实回收站 |
| REC03 | implemented；O：永久删除指定ID的job | 受控通过 |
| REC04 | implemented；O：显式清空job和权限；只读身份403 | 受控通过；禁止本轮真实全局清空 |
| JOB10 | implemented；R：丢失copy/add响应、重启、dry-run不修改、同UK新凭据核验、不同UK拒绝、禁止重发 | 受控故障注入；未对真实百度丢弃写回执 |

## 关键边界与回归

TestCpMvPrepareErrorIsReturnedInsteadOfSuccess分别在copy/move/rename上注入prepare网络错误，确认错误类型/原始cause和请求数，防止原nil成功回归。TestPrepareBDStokenUsesDecimalAppID确保app_id是十进制协议值。TestP5CLIShareExtractionReadsEveryPageAndRejectsRepeatedPages验证205项全部聚合和重复页错误；TestP5SharedTextPasswordPrecedenceAndLegacyRejection覆盖显式/URL/文本优先级；TestP5CoreOfflineQueryRejectsMoreThan100IDsBeforeTransport验证核心不再静默截断。

TestP5IdempotencySurvivesDynamicInspectionAndDeletedPattern覆盖同步分享回执、未知分享禁止重发、转存动态检查信息变化与已删除模式的提交重放。TestP5Offline1000WindowAndNeverFetchSource用1001项证明本地只有1000项窗口，两个本地cursor页只发出一次上游list请求，离线源服务器零访问；原窗口不完整时JOB10仍interrupted且写请求数不增加。

TestP5CancellationKeepsRemoteEffectsAndQueuedItems覆盖A/B独立绑定账号、两执行槽和等待队列：排队取消保留每项none；取消A后B仍running，百度已完成的A第一项不回滚；核验确认第一项并把尚未提交第二项记cancelled，没有第二次网络写。TestP5ShareTransferAuditDownloadsOnlyConfirmedItems验证首项冲突、第二项响应丢失后，核验只下载成功转存项，不下载失败目标也不重发转存；fixture同时验证字符串形式的转存返回ID。TestP5UnknownBaiduIdentityCannotAuthorizeNewCredentialAudit验证UK=0不能当作有效身份授权换版核验。TestP5WriteOwnershipAllowlistAndReadScope验证账号allowlist、owner、write-only可提交写入但不能访问公开读取接口、read-only不能清空。

TestP5MigrationFromV5PreservesCheckpointReceiptAndStage验证v5→v6后上传检查点、paused状态、控制回执、加密暂存元数据和活动引用；v1/v2/v3/v4迁移测试继续通过。TestP5InspectionMergeExpiryAndUnknownMutationReplay验证并发目录展开合并、不延长TTL、过期拒绝、未知同步提交不因清理而重发。原P2旧请求/检查点绑定、P3凭据/撤销/事件/Artifact，以及P4传输控制和源指纹继续纳入总回归。

JOB10并不承诺所有未知事务都可恢复成确定成功。回收站恢复响应丢失实际ID、全局清空回执丢失、离线原窗口不完整或匹配不唯一等保留interrupted；这是不盲目重试的安全结果。普通任务仍绑定旧凭据版本，核验换版必须验证同一百度UK。SHR01同步未知结果保留持久意图，需用只读分享查询人工核对，不通过换Key偷偷重发。

## 真实上游范围

[脱敏报告](http-api-p5-upstream-verification.json)记录linux/amd64、Go1.23.12、单个授权扫码Cookie会话、TLS证书校验、42个检查阶段。测试只在/BaiduPCS-Go-P2-Verification/中新建本轮专用目录和分享，目录与新分享清理通过。文件写操作、新分享生命周期和TRF01成功；TRF02明确上游拒绝单列，未计成功。OFF/REC、已有分享列表、全局清空及故障窗口全部用受控证据，未触及既有日常资源。报告不包含账号标识、文件名、分享ID、Cookie、STOKEN或签名URL。

首轮overwrite大小断言曾将32字节误写为31，修正为由fixture长度计算后真实核验通过；分享检查首次遇到上游字符串fs_id，适配并加入精度回归后通过。失败轮次资源也经defer清理。最大规模、跨真实账号及成功转存/下载子任务没有被扩写为真实百度证据。

## 总检查与复现

[机器验收摘要](http-api-p5-verification.json)与[13目标构建报告](http-api-p5-build-verification.json)可独立查阅。

相关完整race范围均已通过（首次仅旧能力计数断言仍为51/54，更新为74/31后整个bootstrap包重新race通过；其余包首次通过）。新增不完整离线窗口核验/owner边界的受影响race也通过。151组实际HTTP响应覆盖全部74个implemented操作，105操作/89路径/191schema、67个边界例、25个行为规格（10组已有运行证据链接）及622条AST映射通过。14个CLI离线包与baidupcs核心回归通过。13个CGO=0目标构建及SQLite依赖隔离均通过，见[构建报告](http-api-p5-build-verification.json)；仅linux/amd64作本轮运行验证。矩阵后增加与平台无关的UK>0身份校验及核验后下载子任务只选择已确认成功项，无平台代码/import/依赖变化；受影响P5 race另行通过后保持构建结论，不重复全量交叉编译。已知原downloader测试编译失败、固定开发者路径和外网示例仍按[P0基线](http-api-p0-baseline.md)单列，不使用无筛选go test ./...代替验收。

```sh
export PATH=/tmp/baidupcs-toolchain/bin:$PATH
BAIDUPCS_P1_CONTRACT_RESPONSES=/tmp/p1.json \
BAIDUPCS_P2_CONTRACT_RESPONSES=/tmp/p2.json \
BAIDUPCS_P3_ACCOUNT_RESPONSES=/tmp/p3-accounts.json \
BAIDUPCS_P3_CONTRACT_RESPONSES=/tmp/p3.json \
BAIDUPCS_P4_CONTRACT_RESPONSES=/tmp/p4.json \
BAIDUPCS_P5_FILE_RESPONSES=/tmp/p5-files.json \
BAIDUPCS_P5_SHARE_RESPONSES=/tmp/p5-shares.json \
BAIDUPCS_P5_BUSINESS_RESPONSES=/tmp/p5-business.json \
BAIDUPCS_P5_RECONCILE_RESPONSES=/tmp/p5-reconcile.json \
go test -p=1 -race -count=1 -timeout=600s \
  ./internal/domain/... ./internal/application/... ./internal/infrastructure/... \
  ./internal/httpapi ./internal/bootstrap . ./baidupcs
/tmp/baidupcs-p2-venv/bin/python api/openapi/tools/check_contract.py --live-responses \
  /tmp/p1.json /tmp/p2.json /tmp/p3-accounts.json /tmp/p3.json /tmp/p4.json \
  /tmp/p5-files.json /tmp/p5-shares.json /tmp/p5-business.json /tmp/p5-reconcile.json
go test -count=1 -timeout=120s ./pcsliner/args ./pcsutil/cachepool \
  ./pcsutil/converter ./pcsutil/escaper ./pcsutil/waitgroup ./pcsutil/taskframework \
  ./baidupcs/netdisksign ./baidupcs/expires/cachemap ./requester ./requester/rio \
  ./requester/rio/speeds ./requester/uploader ./internal/pcscommand ./internal/pcsfunctions/pcsupload
python3 api/openapi/tools/check_builds.py --report /tmp/p5-builds.json
# 仅在获准实测环境单独启用，不打印.env，不开启shell tracing。
set -a
. ./.env
set +a
BAIDUPCS_P5_LIVE_REPORT=/tmp/p5-live.json \
go test -count=1 -timeout=600s ./internal/bootstrap -run '^TestP5UpstreamDedicatedAccount$'
```

CI保存所有九组响应文件，按实际handler验证全部已开放操作；CLI离线范围继续为14个历史包，另含核心baidupcs回归。完整race与交叉编译分开执行，避免SQLite fsync受交叉编译并发I/O干扰。升级与备份/回滚需按[P5指南](http-api-p5-guide.md)保持密钥、DB、artifacts和staging一致。
