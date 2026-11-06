# HTTP API P7 全部105项逐操作报告

P6实现 `0273055`，P7实现 `e3a00f6`，最终校正 `7607dfc`。契约 `0.7.0-p7`，SQLite v7；105 implemented / 0 partial / 0 planned。14组生产handler实际响应覆盖全部105项，217条响应；INT06以类型化503核销。源码622/622映射及全部25条行为规格关联分别统计。

机器可读的全部响应组/状态、测试文件及函数、原源码映射数量和历史上游阶段见 [operations.json](http-api-p7-operations.json)。复现与运行结果见 [P7验收](http-api-p7-verification.md)，提交见 [发布记录](http-api-p7-verification.json)。

受控通过只代表实现按契约执行。表中“真实流程中观察”含账号/任务/artifact等本地服务操作，不表示每次调用都访问百度。单Cookie样本不扩展到其它账号类型或参数；P2恢复仅代表当时受限单文件。P6 INT02旧解析器误报成功已撤回，P5同账号TRF02被拒绝；完整登录确认、增量、离线和回收站仅受控。

| ID | 方法与路径 | 阶段 / 受控 | 真实范围 |
| --- | --- | --- | --- |
| ACC01 | `POST /api/v1/accounts` | P3 / 通过 (3) | [真实流程中观察（含本地操作）](http-api-p3-upstream-verification.json) |
| ACC02 | `GET /api/v1/accounts` | P3 / 通过 (3) | [真实流程中观察（含本地操作）](http-api-p3-upstream-verification.json) |
| ACC03 | `GET /api/v1/accounts/{account_id}` | P3 / 通过 (4) | [真实流程中观察（含本地操作）](http-api-p3-upstream-verification.json) |
| ACC04 | `PATCH /api/v1/accounts/{account_id}` | P3 / 通过 (3) | [真实流程中观察（含本地操作）](http-api-p3-upstream-verification.json) |
| ACC05 | `DELETE /api/v1/accounts/{account_id}` | P3 / 通过 (1) | [真实流程中观察（含本地操作）](http-api-p3-upstream-verification.json) |
| ACC06 | `PATCH /api/v1/accounts/{account_id}/credentials` | P3 / 通过 (5) | [真实流程中观察（含本地操作）](http-api-p3-upstream-verification.json) |
| ACC07 | `POST /api/v1/accounts/{account_id}/validate` | P3 / 通过 (3) | [限定样本成功](http-api-p3-upstream-verification.json) |
| ACC08 | `GET /api/v1/accounts/{account_id}/quota` | P3 / 通过 (1) | [限定样本成功](http-api-p3-upstream-verification.json) |
| ACC09 | `GET /api/v1/accounts/{account_id}/capabilities` | P3 / 通过 (1) | [真实流程中观察（含本地操作）](http-api-p3-upstream-verification.json) |
| ART01 | `GET /api/v1/artifacts/{artifact_id}` | P3 / 通过 (2) | [真实流程中观察（含本地操作）](http-api-p3-upstream-verification.json) |
| ART02 | `GET /api/v1/artifacts/{artifact_id}/content` | P3 / 通过 (6) | [真实流程中观察（含本地操作）](http-api-p3-upstream-verification.json) |
| ART03 | `HEAD /api/v1/artifacts/{artifact_id}/content` | P3 / 通过 (2) | [真实流程中观察（含本地操作）](http-api-p3-upstream-verification.json) |
| ART04 | `DELETE /api/v1/artifacts/{artifact_id}` | P3 / 通过 (1) | [真实流程中观察（含本地操作）](http-api-p3-upstream-verification.json) |
| CFG01 | `GET /api/v1/configuration` | P1 / 通过 (3) | 本地服务，无百度步骤 |
| CFG02 | `PATCH /api/v1/configuration` | P1 / 通过 (4) | 本地服务，无百度步骤 |
| CFG03 | `GET /api/v1/configuration/schema` | P1 / 通过 (1) | 本地服务，无百度步骤 |
| CFG04 | `POST /api/v1/configuration/validate` | P1 / 通过 (1) | 本地服务，无百度步骤 |
| CFG05 | `POST /api/v1/configuration/reset` | P1 / 通过 (1) | 本地服务，无百度步骤 |
| CFG06 | `POST /api/v1/configuration/reload` | P6 / 通过 (2) | [仅受控](http-api-p6-upstream-verification.json) |
| CFG07 | `GET /api/v1/configuration/defaults` | P1 / 通过 (1) | 本地服务，无百度步骤 |
| CFG08 | `GET /api/v1/configuration/cli-defaults` | P6 / 通过 (1) | [仅受控](http-api-p6-upstream-verification.json) |
| CFG09 | `PATCH /api/v1/configuration/cli-defaults` | P6 / 通过 (1) | [仅受控](http-api-p6-upstream-verification.json) |
| CTX01 | `POST /api/v1/contexts` | P6 / 通过 (1) | [批准目录单Cookie样本](http-api-p6-upstream-verification.json) |
| CTX02 | `GET /api/v1/contexts/{context_id}` | P6 / 通过 (1) | [批准目录单Cookie样本](http-api-p6-upstream-verification.json) |
| CTX03 | `PATCH /api/v1/contexts/{context_id}` | P6 / 通过 (1) | [批准目录单Cookie样本](http-api-p6-upstream-verification.json) |
| CTX04 | `DELETE /api/v1/contexts/{context_id}` | P6 / 通过 (1) | [批准目录单Cookie样本](http-api-p6-upstream-verification.json) |
| DWN01 | `POST /api/v1/accounts/{account_id}/downloads` | P4 / 通过 (2) | [限定样本成功](http-api-p4-upstream-verification.json) |
| DWN02 | `POST /api/v1/accounts/{account_id}/download-links` | P4 / 通过 (1) | [限定样本成功](http-api-p4-upstream-verification.json) |
| DWN03 | `GET /api/v1/accounts/{account_id}/content` | P4 / 通过 (3) | [限定样本成功](http-api-p4-upstream-verification.json) |
| DWN04 | `HEAD /api/v1/accounts/{account_id}/content` | P4 / 通过 (2) | [限定样本成功](http-api-p4-upstream-verification.json) |
| DWN05 | `POST /api/v1/accounts/{account_id}/download-tickets` | P4 / 通过 (1) | [限定样本成功](http-api-p4-upstream-verification.json) |
| EXP01 | `POST /api/v1/accounts/{account_id}/exports` | P6 / 通过 (1) | [批准目录单Cookie样本](http-api-p6-upstream-verification.json) |
| FIL01 | `GET /api/v1/accounts/{account_id}/files` | P3 / 通过 (16) | [限定样本成功](http-api-p3-upstream-verification.json) |
| FIL02 | `GET /api/v1/accounts/{account_id}/files/metadata` | P3 / 通过 (1) | [限定样本成功](http-api-p3-upstream-verification.json) |
| FIL03 | `POST /api/v1/accounts/{account_id}/files/metadata` | P3 / 通过 (1) | [限定样本成功](http-api-p3-upstream-verification.json) |
| FIL04 | `GET /api/v1/accounts/{account_id}/files/search` | P3 / 通过 (5) | [限定样本成功](http-api-p3-upstream-verification.json) |
| FIL05 | `POST /api/v1/accounts/{account_id}/file-scans` | P3 / 通过 (1) | [限定样本成功](http-api-p3-upstream-verification.json) |
| FIL06 | `POST /api/v1/accounts/{account_id}/path-matches` | P3 / 通过 (1) | [限定样本成功](http-api-p3-upstream-verification.json) |
| FIL07 | `POST /api/v1/accounts/{account_id}/directories` | P5 / 通过 (1) | [限定样本成功](http-api-p5-upstream-verification.json) |
| FIL08 | `POST /api/v1/accounts/{account_id}/file-deletions` | P5 / 通过 (1) | [限定样本成功](http-api-p5-upstream-verification.json) |
| FIL09 | `POST /api/v1/accounts/{account_id}/file-copies` | P5 / 通过 (1) | [限定样本成功](http-api-p5-upstream-verification.json) |
| FIL10 | `POST /api/v1/accounts/{account_id}/file-moves` | P5 / 通过 (1) | [限定样本成功](http-api-p5-upstream-verification.json) |
| FIL11 | `POST /api/v1/accounts/{account_id}/file-renames` | P5 / 通过 (1) | [限定样本成功](http-api-p5-upstream-verification.json) |
| FIL12 | `GET /api/v1/accounts/{account_id}/file-changes` | P6 / 通过 (1) | [仅受控](http-api-p6-upstream-verification.json) |
| INT01 | `POST /api/v1/accounts/{account_id}/file-checksums/inspect` | P6 / 通过 (1) | [批准目录单Cookie样本](http-api-p6-upstream-verification.json) |
| INT02 | `POST /api/v1/accounts/{account_id}/file-checksums/fix` | P6 / 通过 (1) | [严格errno解析后被拒绝](http-api-p6-upstream-verification.json) |
| INT03 | `POST /api/v1/local/checksums` | P6 / 通过 (1) | [本地操作，无百度步骤](http-api-p6-upstream-verification.json) |
| INT04 | `POST /api/v1/accounts/{account_id}/file-verifications` | P6 / 通过 (1) | [批准目录单Cookie样本](http-api-p6-upstream-verification.json) |
| INT05 | `POST /api/v1/accounts/{account_id}/file-checksums/from-link` | P6 / 通过 (2) | [批准目录单Cookie样本](http-api-p6-upstream-verification.json) |
| INT06 | `POST /api/v1/accounts/{account_id}/legacy/rapid-uploads` | P6 / 通过 (1) | [长期不可用语义已实现](http-api-p6-upstream-verification.json) |
| JOB01 | `GET /api/v1/jobs` | P3 / 通过 (1) | [真实流程中观察（含本地操作）](http-api-p3-upstream-verification.json) |
| JOB02 | `GET /api/v1/jobs/{job_id}` | P3 / 通过 (3) | [真实流程中观察（含本地操作）](http-api-p3-upstream-verification.json) |
| JOB03 | `GET /api/v1/jobs/{job_id}/items` | P3 / 通过 (3) | [真实流程中观察（含本地操作）](http-api-p3-upstream-verification.json) |
| JOB04 | `GET /api/v1/jobs/{job_id}/result` | P3 / 通过 (12) | [真实流程中观察（含本地操作）](http-api-p3-upstream-verification.json) |
| JOB05 | `GET /api/v1/jobs/{job_id}/events` | P3 / 通过 (4) | [真实流程中观察（含本地操作）](http-api-p3-upstream-verification.json) |
| JOB06 | `POST /api/v1/jobs/{job_id}/cancel` | P3 / 通过 (4) | [仅历史P2单文件子集](http-api-p2-upstream-verification.json) |
| JOB07 | `POST /api/v1/jobs/{job_id}/pause` | P4 / 通过 (1) | 仅受控 |
| JOB08 | `POST /api/v1/jobs/{job_id}/resume` | P4 / 通过 (3) | [仅历史P2单文件子集](http-api-p2-upstream-verification.json) |
| JOB09 | `POST /api/v1/jobs/{job_id}/retry` | P3 / 通过 (2) | [真实部分失败/retry流程](http-api-p3-upstream-verification.json) |
| JOB10 | `POST /api/v1/jobs/{job_id}/reconcile` | P5 / 通过 (2) | [仅受控](http-api-p5-upstream-verification.json) |
| JOB11 | `GET /api/v1/jobs/{job_id}/artifacts` | P3 / 通过 (1) | 本地服务，无百度步骤 |
| LOC01 | `GET /api/v1/local/roots` | P3 / 通过 (1) | 本地服务，无百度步骤 |
| LOC02 | `POST /api/v1/local/encryptions` | P6 / 通过 (1) | [本地操作，无百度步骤](http-api-p6-upstream-verification.json) |
| LOC03 | `POST /api/v1/local/decryptions` | P6 / 通过 (1) | [本地操作，无百度步骤](http-api-p6-upstream-verification.json) |
| LOG01 | `POST /api/v1/account-login-sessions` | P6 / 通过 (7) | [仅受控](http-api-p6-upstream-verification.json) |
| LOG02 | `GET /api/v1/account-login-sessions/{session_id}` | P6 / 通过 (1) | [仅受控](http-api-p6-upstream-verification.json) |
| LOG03 | `GET /api/v1/account-login-sessions/{session_id}/qrcode` | P6 / 通过 (2) | [仅受控](http-api-p6-upstream-verification.json) |
| LOG04 | `POST /api/v1/account-login-sessions/{session_id}/responses` | P6 / 通过 (9) | [仅受控](http-api-p6-upstream-verification.json) |
| LOG05 | `POST /api/v1/account-login-sessions/{session_id}/complete` | P6 / 通过 (7) | [仅受控](http-api-p6-upstream-verification.json) |
| LOG06 | `DELETE /api/v1/account-login-sessions/{session_id}` | P6 / 通过 (6) | [仅受控](http-api-p6-upstream-verification.json) |
| OFF01 | `POST /api/v1/accounts/{account_id}/offline-tasks` | P5 / 通过 (1) | [仅受控](http-api-p5-upstream-verification.json) |
| OFF02 | `GET /api/v1/accounts/{account_id}/offline-tasks` | P5 / 通过 (1) | [仅受控](http-api-p5-upstream-verification.json) |
| OFF03 | `POST /api/v1/accounts/{account_id}/offline-tasks/query` | P5 / 通过 (2) | [仅受控](http-api-p5-upstream-verification.json) |
| OFF04 | `POST /api/v1/accounts/{account_id}/offline-tasks/cancellations` | P5 / 通过 (1) | [仅受控](http-api-p5-upstream-verification.json) |
| OFF05 | `POST /api/v1/accounts/{account_id}/offline-tasks/deletions` | P5 / 通过 (1) | [仅受控](http-api-p5-upstream-verification.json) |
| OFF06 | `POST /api/v1/accounts/{account_id}/offline-tasks/clear` | P5 / 通过 (1) | [仅受控](http-api-p5-upstream-verification.json) |
| OFF07 | `GET /api/v1/accounts/{account_id}/offline-tasks/{offline_task_id}` | P5 / 通过 (1) | [仅受控](http-api-p5-upstream-verification.json) |
| REC01 | `GET /api/v1/accounts/{account_id}/recycle` | P5 / 通过 (1) | [仅受控](http-api-p5-upstream-verification.json) |
| REC02 | `POST /api/v1/accounts/{account_id}/recycle/restorations` | P5 / 通过 (1) | [仅受控](http-api-p5-upstream-verification.json) |
| REC03 | `POST /api/v1/accounts/{account_id}/recycle/deletions` | P5 / 通过 (1) | [仅受控](http-api-p5-upstream-verification.json) |
| REC04 | `POST /api/v1/accounts/{account_id}/recycle/clear` | P5 / 通过 (1) | [仅受控](http-api-p5-upstream-verification.json) |
| SHR01 | `POST /api/v1/accounts/{account_id}/shares` | P5 / 通过 (1) | [限定样本成功](http-api-p5-upstream-verification.json) |
| SHR02 | `GET /api/v1/accounts/{account_id}/shares` | P5 / 通过 (1) | [仅受控](http-api-p5-upstream-verification.json) |
| SHR03 | `GET /api/v1/accounts/{account_id}/shares/{share_id}` | P5 / 通过 (1) | [限定样本成功](http-api-p5-upstream-verification.json) |
| SHR04 | `POST /api/v1/accounts/{account_id}/share-cancellations` | P5 / 通过 (1) | [限定样本成功](http-api-p5-upstream-verification.json) |
| SYS01 | `GET /healthz` | P1 / 通过 (1) | 本地服务，无百度步骤 |
| SYS02 | `GET /readyz` | P1 / 通过 (1) | 本地服务，无百度步骤 |
| SYS03 | `GET /api/v1/system` | P1 / 通过 (1) | 本地服务，无百度步骤 |
| SYS04 | `GET /api/v1/capabilities` | P1 / 通过 (1) | 本地服务，无百度步骤 |
| SYS05 | `GET /api/v1/diagnostics/network` | P6 / 通过 (2) | [仅受控](http-api-p6-upstream-verification.json) |
| SYS06 | `GET /api/v1/accounts/{account_id}/diagnostics/pcs-servers` | P6 / 通过 (1) | [批准目录单Cookie样本](http-api-p6-upstream-verification.json) |
| SYS07 | `GET /metrics` | P7 / 通过 (1) | 本地服务，无百度步骤 |
| SYS08 | `GET /api/v1/openapi.json` | P1 / 通过 (1) | 本地服务，无百度步骤 |
| SYS09 | `GET /docs` | P1 / 通过 (1) | 本地服务，无百度步骤 |
| TRF01 | `POST /api/v1/accounts/{account_id}/share-inspections` | P5 / 通过 (3) | [限定样本成功](http-api-p5-upstream-verification.json) |
| TRF02 | `POST /api/v1/accounts/{account_id}/share-transfers` | P5 / 通过 (1) | [同账号转存被拒绝](http-api-p5-upstream-verification.json) |
| UPL01 | `POST /api/v1/accounts/{account_id}/uploads` | P4 / 通过 (2) | [限定样本成功](http-api-p4-upstream-verification.json) |
| UPL02 | `POST /api/v1/staging-files` | P4 / 通过 (3) | [真实流程中观察（含本地操作）](http-api-p4-upstream-verification.json) |
| UPL03 | `GET /api/v1/staging-files/{file_id}` | P4 / 通过 (1) | [真实流程中观察（含本地操作）](http-api-p4-upstream-verification.json) |
| UPL04 | `DELETE /api/v1/staging-files/{file_id}` | P4 / 通过 (2) | [真实流程中观察（含本地操作）](http-api-p4-upstream-verification.json) |
| UPL05 | `POST /api/v1/staging-upload-sessions` | P6 / 通过 (1) | [本地操作，无百度步骤](http-api-p6-upstream-verification.json) |
| UPL06 | `GET /api/v1/staging-upload-sessions/{session_id}` | P6 / 通过 (1) | [本地操作，无百度步骤](http-api-p6-upstream-verification.json) |
| UPL07 | `PATCH /api/v1/staging-upload-sessions/{session_id}/content` | P6 / 通过 (2) | [本地操作，无百度步骤](http-api-p6-upstream-verification.json) |
| UPL08 | `POST /api/v1/staging-upload-sessions/{session_id}/complete` | P6 / 通过 (1) | [本地操作，无百度步骤](http-api-p6-upstream-verification.json) |
| UPL09 | `DELETE /api/v1/staging-upload-sessions/{session_id}` | P6 / 通过 (1) | [本地操作，无百度步骤](http-api-p6-upstream-verification.json) |

## 统一保留的边界

所有任务仍绑定提交时账号、路径、凭据版本和有效选项；普通更新保留旧版本，null撤销。未知结果interrupted、只读核验、禁止自动重发和不冒充远端回滚保持；UK=0不授权换版核验。retry新建与重新准备、旧P2序列化/job_id/检查点、持久事件/游标/artifact/staging引用均保留。

上下文在全部受影响入口冻结，不共享CLI全局当前账号；登录complete仅用已保存核验的交换态；本地密钥不跨重启；接收offset由fsync+DB控制并与百度断点独立。NoSplitFile/Rname/SuperTransfer非默认仍明确拒绝。

每个操作的原CLI/配置/选项来源见 [622项清单](../api/openapi/source-inventory.json)和[覆盖矩阵](http-api-coverage.md)。新增HTTP服务操作不要求虚构旧CLI对应项。停机一致备份/迁移/回滚见[P7指南](http-api-p7-guide.md)。
