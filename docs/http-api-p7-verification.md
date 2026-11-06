# HTTP API P7 验收

契约 `0.7.0-p7`，SQLite schema v7；105 implemented / 0 partial / 0 planned。P6 实现提交 `0273055`，P7 实现提交 `e3a00f6`，最终能力/备份校正 `7607dfc`。本轮运行 Linux/amd64、Go 1.23.12。完整结果、时间和命令见 [验收 JSON](http-api-p7-verification.json)，逐项业务与上游范围见 [105 操作报告](http-api-p7-operations.md)。

## 分开统计的结论

| 项目 | 结论与证据 |
| --- | --- |
| 源码映射 | 622/622：47 叶子命令、5 命令组、90 命令参数、1 全局参数、24 配置字段、45 业务选项字段、410 函数/方法；[清单](../api/openapi/source-inventory.json)和[矩阵](http-api-coverage.md)逐项对应 |
| HTTP 实现 | 105/105；本次 P6 30 项、P7 SYS07；所有既有 P2—P5 操作保留 |
| 契约 | 105 operation、89 path、191 schema；105 组合成交换、67 schema 边界、25 条行为规格全部关联离线测试；14 组生产 handler 实际响应覆盖全部开放操作 |
| 受控执行 | 使用真实应用服务、SQLite、受限文件系统及本地 HTTP/TLS 协议 fixture，包含错误/取消/重启窗口；不是百度实测 |
| 真实上游 | 沿用 P2—P6 的时间/账号/参数范围；P3 33 阶段、P4 39 阶段；P5 转存、P6 摘要修复的拒绝分别保留；不计算全部 105 项的“百度成功率” |
| 平台 | 最终原生二进制与恢复演练仅实际运行 Linux/amd64；P6 的 13 个 CGO=0 目标构建证据复用，不宣称其他目标机已运行 |

`INT06` 的合法输入以明确 503 `UPSTREAM_CAPABILITY_UNAVAILABLE`、retryable=false 核销，能力 availability=upstream_unavailable；没有用 planned 占位或假成功 job。LOG01—LOG06 已实现，真实密码/短信/邮件/扫码确认仅受控验收；未重复要求用户扫码。所有105项的实现完成与真实上游可用性独立。

## P7 新增验收

- `TestP7MetricsAuthorizationBoundedLabelsAndNonblockingScrape`：无 token 401、reader 403、admin 200、查询参数 400、未知路由归入 unmatched、无秘密或动态 ID 标签，输出完整 Prometheus media type；实际响应导出用于契约。
- `TestBoundedLabelsAndConcurrentCounters`：12 个 goroutine 并发计数/抓取，1200 次受信操作及 1200 个不同未知输入只进入固定集合，确认计数和传输字节。
- `TestMetricsSnapshotDoesNotWaitForExecutorMutex`：持有执行 mutex 时仍可立即读取队列和执行位；抓取代码没有数据库或执行器锁依赖。
- `TestP7SettledJobCountersAndExpiredCredentialBoundary`：真实工具任务收尾被计数，INT03无需百度凭据且capability为available，百度失效为 422，服务账号元数据与任务查询仍可 200。
- `TestP7TicketExpiryCurrentPermissionsAndCredentialRevocation`：有效票据、过期 HEAD、重启后的 owner allowlist 变化、凭据撤销；权限按当前身份重新检查。
- `TestExplicitZeroAndInverseOptionsOverrideConfiguration`：显式 checksum/check_filename=true 覆盖逆向配置，retry=0/rate="0" 保持，省略字段仍继承。
- `TestP7RsyncComparesSizeAndDryRunHasNoTransfer`：同大小不同内容仍按 rsync 跳过，dry_run 不传输/不写；full 层级、test 实传但 written_bytes=0，并验证单文件 HTTP 投影。
- `TestP7MigrationFromV6PreservesUnknownWriteCheckpointReceiptAndStage`：v6 任务、私有检查点/冻结计划、控制回执、SHR01 未知意图、staging 及引用迁移后保留，未知意图不能重放。
- `TestP7ResourceTombstoneCleanupIsBoundedAndKeepsActiveRows`：旧 tombstone 可清理，活动和近一天 tombstone 保留；SQL 每批最多 512，原 staging 引用独立保护。
- [恢复演练](http-api-p7-recovery-verification.json)：实际当前/旧二进制、运行中锁拒绝、含非空 WAL/SHM 的完整拷贝、损坏/符号链接/FIFO/恶意manifest/覆盖拒绝、ENOSPC 清理、接收 offset 恢复与摘要完成幂等、旧二进制拒绝 v7且DB不变，以及恢复升级前 v6 快照后实际运行旧版本。

新增回归发现并修复了 test=true 下载的写入字节误记。首次完整 race 还暴露了终态可见与执行器排空之间的窗口：指标现在在终态提交前获取结果证据，提交后只更新原子计数器；核验测试仅等待明确且未产生写入的“原执行器仍在排空”409，未放宽 JOB10 的实际保护。受影响测试连续 3 次 race 通过。另一次完整回归遇到历史下载恢复fixture的8秒完成上限；已将其调整为30秒，保留相同状态、确认Range和逐字节断点断言，不放宽生产超时或取消约束。随后完整范围重新执行并通过；最终INT03能力元数据修正单独完成受影响bootstrap race与实际响应重导出，前次失败不作为最终通过证据。

## 稳定性与历史证据汇总

| 验收边界 | 已执行测试/证据 |
| --- | --- |
| 2 执行 / 16 等待、重复提交 | transfers `TestQueueBoundAndConcurrentSubmissionReplay`、`TestDurableAdmissionRepairsLostSubmissionAndRestart` |
| 多账号、目标锁、取消/暂停/恢复 | baidu `TestSingleFileTransferRoundTripAndCancellationIsolation`、`TestP4PauseResumeIsolationAndChangedSource`；transfers `TestTargetLockCancellationAdmissionAndOwnerPermissions` |
| context/登录/接收并发隔离 | P6 context 冻结/replay、并发 complete、localfs reception CAS；独立调用方/账号/会话 fixture |
| 凭据换版/撤销/未知 UK | P3 账号版本；P5 `TestP5UnknownWriteReconciliationRestartAndNewCredentials`、`TestP5UnknownBaiduIdentityCannotAuthorizeNewCredentialAudit`；P7 票据权限 |
| 未知远端结果、取消不回滚 | P2 丢响应核验、P5 写入/分享意图、取消后逐项效果与 JOB10 只读核验；核验下载子任务只包含确认成功项 |
| 进程关闭/重启/SIGKILL | P2 HTTP 子进程四个故障窗口与 SIGTERM；P6 真 HTTP 不完整 PATCH 后 SIGKILL、恢复截断未提交尾部；P7 真旧二进制及快照回滚 |
| 断连/慢上游/慢 SSE | `TestP2SlowDownloadCancellationIsBounded`、`TestP2BodyIdleTimeoutReleasesConnection`、P4 内容慢流断连、P3 SSE 慢消费者/心跳/重放 |
| 大目录、游标/高水位 | P3 1001/2000/2001、重复/失败页与快照限额；`TestP3ItemPaginationHighWaterBeyondTenThousand`；导出节点/深度/时间上限 |
| 磁盘不足、冲突、root 边界 | P2 allocation/commit 故障、P4 staging ENOSPC/并发不覆盖、P6 crypto/reception ENOSPC/错误密钥/gzip/FIFO/symlink、P7 恢复 ENOSPC |
| 总量/数量/TTL/引用 | P3 artifact 配额/TTL/引用，P4 staging，P6 未完成接收预留/offset/hash/TTL；paused/interrupted 引用；P7 tombstone |
| 旧数据/检查点兼容 | persistence v1/v2、v3、v4、v5、v6 迁移测试；P3 保留 P2 Request 空字段序列化/job_id 和原检查点绑定 |

这些是受控故障与真实进程边界，不是硬件断电或所有文件系统的认证。备份脚本只声明 POSIX 支持和 Linux/amd64 的实际运行；Windows 与其它目标机实际运行、账户类型、超大真实数据集、全局离线/回收站操作不在本轮已验证范围。

## 复现

完整 14 个响应环境变量和命令见 [OpenAPI README](../api/openapi/README.md#p1p7-实际响应与持续校验) 与 [.github/workflows/http-api.yml](../.github/workflows/http-api.yml)。核心命令为：

```sh
go test -p=1 -race -count=1 -timeout=600s \
  ./internal/domain/... ./internal/application/... \
  ./internal/infrastructure/... ./internal/httpapi ./internal/bootstrap . ./baidupcs
python3 api/openapi/tools/check_recovery.py --report /tmp/baidupcs-p7-recovery.json
```

随后对 CI 列出的全部14组导出运行 `check_contract.py --live-responses ...`，不带 `--write-inventory`；源码清单更新前先人工审查覆盖矩阵。本轮 venv 为 `/tmp/baidupcs-p2-venv`，Go 为 `/tmp/baidupcs-toolchain/bin/go`。CLI 回归固定 14 个离线包，完整列表在验收 JSON/CI；不使用无筛选 `go test ./...` 掩盖 P0 已知 downloader 测试编译、固定开发者路径和外网示例失败。

13 目标构建继续使用 [P6 构建报告](http-api-p6-build-verification.json)，复用原因与最终原生证据见[P7构建报告](http-api-p7-build-verification.json)；P7 没有更改 build tags、支持平台、module 依赖或 SQLite 选择，只增加标准库指标、已有路径的字节统计和 SQL 清理；已重建最终原生服务与实际旧版本。构建和其它目标机运行结论严格分开。

## 凭据与真实范围

P7 不需要新的百度登录或远端写入，没有读取日常 CLI 配置或调用全局清空。P6 已授权的 `.env` 保留 0600、Git 忽略且未跟踪；验证报告不含任何值。真实操作仅曾触及批准目录的当轮资源，清理见各阶段报告。本次不 push、不部署、不修改主检出独立 QR 工作。
