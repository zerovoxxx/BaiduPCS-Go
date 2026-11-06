# P2 账号与单文件传输操作指南

日期：2026-11-05。契约 `0.2.0-p2`。P2 在 P1 的独立数据目录、Bearer、配置快照和五层边界上接入账号、受限单文件传输和持久任务。完整账号管理、文件读取、SSE、目录及全部传输选项分别在 P3/P4 核销。

## 开放范围

12 个 P1 系统/配置操作保持 implemented；ACC01/02/03/07、UPL01、DWN01、JOB02/04/06/08 为 partial，其余 83 个操作仍 planned，授权后返回 501。partial 可以调用，但不表示完整目标契约或真实百度已验收。`GET /api/v1/capabilities` 的 `supported_option_values` 返回规范字符串形式的固定选项值，`limits` 返回数值文件上限和资源限制；`x-phase` 保留完整验收归属。

- 账号支持 BDUSS、含 BDUSS 的 Cookie 文本、access token 三种互斥输入。导入仅保存凭据，`identity_verified=false`、`login_state=unverified`。账号列表按 allowlist 过滤；未知 UID、UK、用户名、性别和年龄返回 null。
- ACC07 用实例 HTTPS 客户端验证 PCS 容量；Cookie 账号另外读取网盘身份。UK 不冒充 UID。token-only 只证明 PCS 访问，不推断 Cookie 身份或上传下载能力；P2 传输要求 BDUSS。
- 上传一次一个 local 源，下载一次一个绝对网盘文件路径；普通文件为 **1 字节至 32 MiB**。目标父目录和授权本地 root 必须存在；P2 不负责创建网盘目录。两项任务可以执行，另外 16 项等待；满队列返回 429 和 `Retry-After: 1`。
- 受限选项必须在继承配置和默认值后满足以下固定值。省略不会自动改成 P2 固定值，不能执行的候选返回 422；格式、类型、未知字段、重复 key 或 null 错误返回 400。显式 false 和 0 保留含义。

| 上传选项 | P2 值 |
| --- | --- |
| file_concurrency / parallel_per_file | 1 / 1 |
| max_retries / policy | 0 / skip |
| skip_rapid_check | true |
| check_filename / no_split_file | false / false |
| max_rate_bytes_per_second | "0" |

| 下载选项 | P2 值 |
| --- | --- |
| mode / path_mode | pcs / relative |
| parallel / file_concurrency | 1 / 1 |
| max_retries / link_index | 0 / 0 |
| overwrite / verify_checksum | false / true |
| preserve_mtime / executable / test / report_workers | false |
| max_rate_bytes_per_second | "0" |

目录、空文件、批量、staged、dry_run=true、context/base_path、其他模式、并发、限速及重试值均不被静默忽略。上游凭据请求必须 HTTPS；已有配置允许保存 `enable_https=false`，但创建携带凭据的客户端时返回 HTTPS_REQUIRED。`force_login_username` 导入回退不支持。Unix 单文件打开逐级使用目录描述符和 O_NOFOLLOW，P2 不沿目录符号链接打开文件；root 本身在启动时解析为真实目录。不同 root 指向同一目录时，共享实际下载目标锁。

## 启动与导入

按照[服务启动指南](http-api-server.md)构建并启动；服务数据目录、root 和凭据文件保持分离。例如启动配置：

```json
{
  "listen": "127.0.0.1:5299",
  "data_dir": "/private/baidupcs-service",
  "roots": [{"id":"test","path":"/private/baidupcs-transfer","read":true,"write":true}]
}
```

下面使用 `$api_token` 指向的 Bearer 值；真实账号导入 body 放在私有 JSON 文件，再由 curl 读取，避免将凭据值写进命令行。示例文件内容：

```json
{"label":"dedicated-test","credentials":{"kind":"bduss","bduss":"EXAMPLE_ONLY"}}
```

```sh
curl -X POST http://127.0.0.1:5299/api/v1/accounts \
  -H "Authorization: Bearer $api_token" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: import-dedicated-test' --data-binary @/private/account-import.json
curl -H "Authorization: Bearer $api_token" http://127.0.0.1:5299/api/v1/accounts
curl -X POST -H "Authorization: Bearer $api_token" \
  http://127.0.0.1:5299/api/v1/accounts/ACCT_ID/validate
```

导入返回 201、Location、ETag；验证返回账号、valid、checked_at 和可空 Problem。API 的 401 表示服务身份问题，不等同于百度登录失效。响应、日志和事件不返回 BDUSS、Cookie 值、access token、代理密码、uploadid、签名 URL 或本地绝对根目录。

## 上传、下载与轮询

上传 body 中 `target_dir` 是网盘目录；实际目标为该目录加本地源的 basename。下载 `destination.directory` 是 root 内目录；实际目标为该目录加远端 basename。下面选择 root 下 `input.bin`，以及事先创建的网盘专用目录：

```json
{
  "sources":[{"type":"local","root_id":"test","relative_path":"input.bin"}],
  "target_dir":"/BaiduPCS-Go-P2-Verification",
  "options":{"file_concurrency":1,"parallel_per_file":1,"max_retries":0,"policy":"skip","skip_rapid_check":true,"check_filename":false,"no_split_file":false,"max_rate_bytes_per_second":"0"}
}
```

将 body 存为 `/private/upload.json` 后提交：

```sh
curl -X POST http://127.0.0.1:5299/api/v1/accounts/ACCT_ID/uploads \
  -H "Authorization: Bearer $api_token" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: upload-one' --data-binary @/private/upload.json
```

下载 body，假设本地 `out` 已存在：

```json
{
  "selector":{"kind":"paths","values":["/BaiduPCS-Go-P2-Verification/input.bin"]},
  "destination":{"root_id":"test","directory":"out"},
  "options":{"mode":"pcs","path_mode":"relative","parallel":1,"file_concurrency":1,"max_retries":0,"overwrite":false,"verify_checksum":true,"preserve_mtime":false,"executable":false,"link_index":0,"test":false,"report_workers":false,"max_rate_bytes_per_second":"0"}
}
```

以同样方式 POST `/api/v1/accounts/ACCT_ID/downloads`。两种提交都返回 202、Job 和指向 JOB02 的 Location；202 表示记录已入库，不表示文件已完成。重复相同提交 key 读取同一逻辑任务；改变输入返回 409。配置变化不会改变已接受任务快照。查询接口还会核对 owner、账号 allowlist、本地 root allowlist 及原 transfer/local.read 或 local.write 权限，admin 不绕过 allowlist。

```sh
curl -H "Authorization: Bearer $api_token" http://127.0.0.1:5299/api/v1/jobs/JOB_ID
curl -H "Authorization: Bearer $api_token" http://127.0.0.1:5299/api/v1/jobs/JOB_ID/result
curl -X POST http://127.0.0.1:5299/api/v1/jobs/JOB_ID/cancel \
  -H "Authorization: Bearer $api_token" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: cancel-one' --data '{}'
curl -X POST http://127.0.0.1:5299/api/v1/jobs/JOB_ID/resume \
  -H "Authorization: Bearer $api_token" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: resume-one' --data '{}'
```

结果未终结时 JOB04 返回 409。取消和恢复支持空对象及可选 reason；控制 key 与提交 key、其他任务和其他控制操作分开。相同控制 key 重放首次逻辑响应，不重复执行控制。数值计数、字节、配置和凭据版本使用十进制字符串；未知总字节数为 null。进度只统计持久确认的数据，活动任务速率是从首次启动到当前的平均确认速率。skip 命中计为 skipped，实际传输字节为 0。事件链接为完整契约保留，SSE 仍 planned；P2 使用轮询。

## 取消、恢复与存储

排空时停止接受新业务请求，取消实例任务，等待检查点和终态写入，再关闭客户端、DB 和数据目录锁。下载按最多 256 KiB 的单元 Sync 后确认检查点；已写但未确认的尾部在恢复时截断，恢复上游必须返回准确的 206、Content-Range 和长度。请求断开不会取消已入库任务。

重启保留 queued 并重新派发；遗留 running 转 interrupted，已知取消的任务不复活。resume 仅接受 interrupted，重新核对凭据版本、配置快照、账号/目标绑定和源指纹。上传合并或下载最终提交结果未知时保留 interrupted；此时 cancel 返回 RECONCILIATION_REQUIRED，应使用 resume 先核验已有结果，不能假报回滚。已确认成功可以胜过取消请求。

SQLite 以 v1→v2→v3 增量迁移；账号、配置、任务记录、检查点和控制重放数据加密。SQL 状态、时间及 version 是权威字段，每次写检查 RowsAffected。v0 内部检查点按原绑定转换，v1 增加随机临时文件分配；高版本检查点明确拒绝。升级后不能把 v3 DB 交给仅支持 v2 的旧程序；回滚使用升级前数据库和匹配的 master.key 备份。

任务和检查点在 P2 保留，不自动清除恢复证据。提交 key 随任务保留；账号/控制重放至少保留 24 小时，活动任务的控制重放不提前过期；启动清理过期且不绑定活动任务的重放。确认 cancelled 的下载仅清理其加密检查点中登记的随机临时文件；不删除最终目标、不猜测清理旧版可预测临时文件或无记录文件。失败及中断文件保留供核验。目录锁保证同一数据目录只有一个执行所有者。进程 SIGKILL 验收不等于主机掉电一致性承诺。

验收证据及平台范围见[P2 验证记录](http-api-p2-verification.md)。
