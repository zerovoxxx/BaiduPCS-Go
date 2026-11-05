# P4 验收记录

本记录在阶段收尾写入实际执行结果，开发过程中不将测试定义等同于通过。

| 操作 | 实现与验收入口 |
| --- | --- |
| UPL01 | 文件/目录/空文件、三策略、握手/校验/重试：TestP4DirectoryHierarchyEmptyFilesAndOptions、TestP4HandshakesResumeExpiryAndChecksums |
| UPL02 | 并发multipart、独立分配、磁盘失败：TestP4MultipartReferencesAndContentHTTP、TestP4StagingDiskFullNeverPublishesReadyFile |
| UPL03、UPL04 | 元数据、引用保护、删除：TestP4MultipartReferencesAndContentHTTP |
| DWN01 | 层级、并行Range、空目录、mtime/权限、test：TestP4DirectoryHierarchyEmptyFilesAndOptions |
| DWN02 | 按输入直链及错误：TestP4MultipartReferencesAndContentHTTP |
| DWN03、DWN04 | 206/416、多Range、HEAD：TestP4MultipartReferencesAndContentHTTP；慢流/断连/重定向沿用P2受控连接测试 |
| DWN05（独立扩展） | 账号/路径/操作绑定及原权限复核：TestP4MultipartReferencesAndContentHTTP |
| JOB07、JOB08 | 真实排空暂停、恢复、B/C隔离、源变化拒绝：TestP4PauseResumeIsolationAndChangedSource |

P2四个HTTP SIGKILL窗口、取消隔离与P3账号/事件/结果语义继续回归。离线证据、真实百度证据、构建和目标机运行分别记录。

最终验收：相关包完整 race（`-p=1 -timeout=600s`）通过；117组实际HTTP响应覆盖全部51个implemented操作，105操作/89路径/190schema、67边界例、25行为规格和620条源码映射通过。14个既有CLI离线包通过；新增UploadGroup隔离和TaskUnit无全局配置/信号依赖检查通过。13个CGO=0目标构建通过；仅linux/amd64为本轮目标机运行，构建报告见[JSON](http-api-p4-build-verification.json)。并行跑完整race与交叉编译曾因磁盘同步竞争超时；降低并发后的完整检查及最后受影响检查通过，未降低生产持久化级别。

真实百度：[脱敏记录](http-api-p4-upstream-verification.json)，2026-11-05，linux/amd64，Go1.23.12，新扫码Cookie账号，批准目录内39阶段通过，最后专用子目录清理通过。UPL01/02/03/04、DWN01（pcs/locate/stream）、DWN02（direct）、DWN03/04/05均有本轮真实证据。JOB07按实例暂停、JOB08两种握手重启恢复以及uploadid失效/分片错误/源变化/并发/磁盘失败为受控上游证据；P2真实恢复证据保持原始范围，不计作本轮P4实测。秒传阶段证明进行了真实握手并完成任务，不将普通上传回退宣称为秒传命中。

复现：

```sh
export PATH=/tmp/baidupcs-toolchain/bin:$PATH
BAIDUPCS_P1_CONTRACT_RESPONSES=/tmp/p1.json \
BAIDUPCS_P2_CONTRACT_RESPONSES=/tmp/p2.json \
BAIDUPCS_P3_ACCOUNT_RESPONSES=/tmp/p3-accounts.json \
BAIDUPCS_P3_CONTRACT_RESPONSES=/tmp/p3.json \
BAIDUPCS_P4_CONTRACT_RESPONSES=/tmp/p4.json \
go test -p=1 -race -count=1 -timeout=600s \
  ./internal/domain/... ./internal/application/... ./internal/infrastructure/... \
  ./internal/httpapi ./internal/bootstrap .
/tmp/baidupcs-p2-venv/bin/python api/openapi/tools/check_contract.py \
  --live-responses /tmp/p1.json /tmp/p2.json /tmp/p3-accounts.json /tmp/p3.json /tmp/p4.json
python3 api/openapi/tools/check_builds.py --report /tmp/p4-builds.json
# 真实验证仅在授权环境显式启用；不打印.env或开启shell tracing。
set -a
. ./.env
set +a
BAIDUPCS_P4_LIVE_REPORT=/tmp/p4-live.json \
go test -count=1 -timeout=600s ./internal/bootstrap -run '^TestP4UpstreamDedicatedAccount$'
```

v4→v5增量迁移保留旧账号/任务/检查点/控制回执；专门的v4迁移测试、旧v1/v2/v3迁移测试均通过。暂存引用保护paused/interrupted资源，终态同事务延长闲置TTL。源目录遍历拒绝符号链接和FIFO；Unix打开/提交固定目录描述符，mtime修改作用于已打开inode。相关风险和备份/回滚步骤见[P4指南](http-api-p4-guide.md)。
