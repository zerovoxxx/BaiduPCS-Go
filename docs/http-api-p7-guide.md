# HTTP API P7 操作与恢复指南

当前契约 `0.7.0-p7`，SQLite schema v7，105 项 operation 全部 implemented，partial/planned 均为 0。P6 的 30 项见 [P6 指南](http-api-p6-guide.md)，全量证据见 [逐操作报告](http-api-p7-operations.md) 和 [P7 验收](http-api-p7-verification.md)。实现完成不代表百度已开放所有能力，也不把受控上游成功算作真实百度成功。

## 授权指标

`GET /metrics` 使用普通 Bearer，要求 admin；缺身份 401、权限不足 403，不接受查询参数或下载票据。响应 `text/plain; version=0.0.4`，保留请求 ID 和 no-store。

```sh
curl --fail --header "Authorization: Bearer $API_TOKEN" http://127.0.0.1:5299/metrics
```

| 指标 | 含义 |
| --- | --- |
| `baidupcs_jobs_queued` | 最近一次有界派发扫描的 queued 行；通常 100ms 更新，存储阻塞时可能更旧；工作者领取前瞬时可含全部 18 个已接纳任务 |
| `baidupcs_jobs_executing` | 当前已占用的执行位，最多 2；包括等待目标锁的执行 |
| `baidupcs_queue_capacity` / `baidupcs_executor_capacity` | 固定等待 16、执行 2；接纳总预算 18 |
| `baidupcs_http_requests_total` / `baidupcs_http_errors_total` | 按固定 operation 和 1xx—5xx 状态类别计数；错误指 4xx/5xx，包括认证和输入错误 |
| `baidupcs_http_request_duration_seconds_sum` | 每个 operation 已结束请求的累计耗时；流式请求结束时计数 |
| `baidupcs_job_executions_started_total` / `baidupcs_job_executions_settled_total` | 本进程实际开始/持久收尾的执行次数；按固定 kind/state；pause 后 resume 是另一次执行，排队取消不算启动 |
| `baidupcs_transfer_confirmed_bytes_total` | 上传/下载执行收尾时确认记录的增量；单文件按检查点、目录按已完成项汇总作恢复基线；目录的项收尾可能包含重启前已传的部分；不是网络设备流量 |
| `baidupcs_local_written_bytes_total` | 执行收尾记录的 written_bytes 增量；下载 test 模式为 0；不是所有磁盘 I/O 的计数 |
| `baidupcs_storage_errors_total` | 派发与执行持久化路径观察到的错误次数；没有错误文本标签 |
| `baidupcs_process_uptime_seconds` | 本进程存活时间，帮助识别重启和计数器归零 |

标签集合在创建时固定：105 个受信契约 operation 加 unmatched，24 个任务 kind（含 other）、7 个收尾 state（含 other）、两种传输 direction。未知输入不会新增标签。没有 job/account/owner ID、路径、URL、Cookie、密钥、任意上游错误。抓取只读取原子计数器、队列样本和执行通道长度，不查历史数据库、不访问网络、不取得执行互斥锁。计数器仅覆盖当前进程已观察的执行；不能据此重建所有历史成功、排队取消或崩溃前未收尾的字节。

## 升级前停机备份

工具 [backup_service.py](../api/openapi/tools/backup_service.py) 仅用于 POSIX 停机操作，本轮实际运行 Linux/amd64。Windows 使用相同的完整备份单位和停机要求，由平台备份工具执行；此脚本没有 Windows 运行结论。

1. 停止接收新写入，正常停止服务并等待退出。使用固定 legacy_accounts_file 的环境，也应停止修改该文件的 CLI 进程。
2. 确认原二进制版本及启动配置；备份启动 JSON、外部 token/TLS 文件、显式 legacy_accounts_file 和必要的授权 local roots。它们可能在数据目录之外，工具不会自行搜索或复制外部路径。保存原目录映射及权限，备份中包含秘密，限制为 0700/0600。
3. 将整个服务数据目录作为一个单位备份：`master.key`、`service.db`、存在的 SQLite WAL/SHM/journal、身份文件、`artifacts/`、`staging/` 以及续传接收的字节。接收 offset/身份/状态在加密 DB，未完成接收数据也在 staging，不能漏掉。

```sh
umask 077
python3 api/openapi/tools/backup_service.py backup /srv/baidupcs-http /backup/baidupcs-before-upgrade
```

源目录必须私有，目标目录必须尚不存在且与源分离。工具取得服务使用的 `service.lock` 非阻塞排他锁，运行中的服务使备份失败；拒绝符号链接、FIFO 和其他特殊文件。复制后验证全部文件的长度与 SHA256、SQLite quick_check/schema，写入私有 manifest，fsync 文件与目录。它不会导出凭据明文或降低 schema。manifest 用于完整性检查，不提供对恶意修改的密码学认证；应保存于受控备份存储。

工具失败只清理它新建的目标，不覆盖已有目标，也不替换源。锁防止本服务同时运行，操作方仍须确保没有绕过服务锁的其他数据库/文件写入者。

## 恢复与回滚

```sh
umask 077
python3 api/openapi/tools/backup_service.py restore /backup/baidupcs-before-upgrade /srv/baidupcs-restored
```

恢复先校验 manifest、路径、所有内容和 schema，再建立新的私有目录；不会覆盖已有服务目录。恢复后使用匹配的二进制及固定启动配置，指向新目录启动，检查 `/healthz`、带凭据的 `/readyz`、账号与配置 revision、任务状态、暂存接收 offset、artifact 和 local root 映射，再将调用方切换到恢复实例。不要让两个实例并行处理同一备份的未知远端写意图。

从 v1—v6 打开时依次增量迁移到 v7；任务、原检查点/控制回执、凭据版本、staging 引用保持。运行中任务重启按原规则 interrupted；本地加解密的临时密钥不会随备份恢复，缺失时明确 `TEMPORARY_SECRET_LOST`，不能用空密钥续跑。交互登录交换态同样不跨重启保留。

旧二进制拒绝较高版本数据库。回滚应停止新服务，恢复升级前完整快照到新目录，配回原二进制与原外部配置；不能修改 `PRAGMA user_version` 欺骗旧程序。恢复旧快照不会回滚已经提交到百度的效果；未知任务继续按原只读核验与禁止自动重发规则处理。

可执行的离线演练如下，只使用临时数据和 loopback，不读取 `.env`、CLI 日常配置或访问百度：

```sh
python3 api/openapi/tools/check_recovery.py --report /tmp/baidupcs-recovery.json
```

演练从交接提交 `83f6a83` 的源码构建真实 schema v6 二进制，因此本地/CI 需要该提交的 Git 历史。临时源码位于当前 worktree 下的隐藏目录，结束删除，不改主检出。

## 实际边界

原 NoSplitFile、Rname/SuperTransfer 非默认值仍明确拒绝；INT06 合法请求固定 503 且 retryable=false。SYS04 的 implemented 与 availability 独立；未配置的 CLI 默认值写入/重载来源明确 disabled，交互登录和未全局验证的百度能力保持 unverified。

真实百度证据沿用 P2—P6 的授权 Cookie/批准目录。P6 INT02 在严格检查 errno 后被百度拒绝，旧解析器忽略 errno 的成功结论已撤回；P5 同账号 TRF02 被拒绝。密码、短信、邮件、扫码确认、账户级增量、离线和回收站的完整流程使用受控证据，没有新增实测成功声明。跨目标构建通过仅说明可编译，本轮运行平台仅 Linux/amd64。
