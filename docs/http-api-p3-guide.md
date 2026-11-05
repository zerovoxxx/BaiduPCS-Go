# P3 账号、读取、任务和结果文件

契约 `0.3.0-p3`。P3 完整实现 ACC01—ACC09、FIL01—FIL06、JOB01—JOB06、JOB09、JOB11、ART01—ART04、LOC01；系统/配置的 P1 操作继续可用。当前为 **40 implemented、3 partial、62 planned**。UPL01、DWN01、JOB08 保留 P2 已验证的单文件范围，完整传输选项仍在 P4。运行方式见[服务指南](http-api-server.md)，逐项证据见[P3 验收](http-api-p3-verification.md)。

以下路径省略 `/api/v1`，所有调用都需要 Authorization Bearer。凭据内容从私有文件提交，不写命令行或日志。

用户已授权在扫码成功后将真实验收凭据保留在本工作区权限为0600的`.env`，由Git忽略，供后续专用目录验证复用；服务不会自动加载它。`BAIDUPCS_P3_CREDENTIALS_JSON`保存API导入输入，`BAIDUPCS_P3_COOKIE_JAR_JSON`保留扫码会话的Cookie域/路径信息；不得把整个Jar直接当作Cookie头跨域发送。真实测试仍需显式设置批准目录，复现命令见[P3验收](http-api-p3-verification.md)。其他机器没有该私有文件时需另行取得凭据。

## 账号和凭据

- `POST /accounts` 继续接受三种互斥主凭据：bduss、cookies、access_token。`force_login_username` 作为未验证的显示名称，不生成百度 UID。
- `GET /accounts`、`GET /accounts/{id}` 返回脱敏信息、账号 revision 和 credential_revision；未知 UID、UK、性别和年龄为 null。
- `PATCH /accounts/{id}` 更新 label；`PATCH /accounts/{id}/credentials` 更新凭据。两个 PATCH 均使用账号 ETag 作为 If-Match，缺失 428、不匹配 412。成功更新返回新 ETag，幂等重放保留首次 ETag。
- 凭据字段缺失表示保留，null 表示清除，空字符串拒绝。cookies 字符串替换唯一 Cookie Jar；不能与单独 Cookie 字段同次提交。`cookies:null` 清除 Cookie 类凭据、保留独立 access_token；`bduss:null` 同时移除依赖它的 Cookie 会话。
- 普通更新归档旧凭据版本，已提交任务继续使用其固定版本。显式清除会撤销此前版本、销毁其秘密载荷，取消对应任务/同步读取的上游连接。删除账号也撤销全部版本；存在活动/待核验任务时返回409 ACCOUNT_IN_USE，须先处理任务，原终态摘要仍可按原权限查询。
- 中断任务的远端提交结果不明时，清除凭据不会把它伪报为失败或已回滚；保留 interrupted，不允许通过 retry 盲目重放。
- `POST /accounts/{id}/validate` 不删除失效凭据；服务 API 的 401 与百度凭据失效的 422 分开。`GET .../quota` 返回字符串字节数和采集时间。
- `GET .../capabilities` 根据当前凭据禁用不具备的能力。`credential_alternatives` 表达至少满足一个凭据组合；token-only 可使用已实现的 PCS token 验证/配额，Cookie 文件能力保持 disabled。已实现不自动代表百度上游 available；verified_at 没有实测依据时为 null。

凭据 PATCH 示例：

```http
PATCH /api/v1/accounts/acct_example/credentials
Authorization: Bearer EXAMPLE_ONLY
If-Match: "3"
Idempotency-Key: credential-update-example
Content-Type: application/json

{"stoken":null,"access_token":"EXAMPLE_ONLY"}
```

## 显式导入旧账号配置

服务启动文件可提供 `legacy_accounts_file`，相对路径按该启动文件所在目录解析。只读取这一固定来源的 baidu_user_list；不初始化 CLI、不采用其当前账号、不导入其他客户端参数、不改写源文件。源文件必须是至多 1 MiB 的普通 JSON 文件。

```json
{"data_dir":"./private/service","legacy_accounts_file":"./private/pcs_config.json"}
```

导入先校验完整凭据集合；旧 COOKIES 与独立字段冲突会拒绝。规范化凭据和条目索引确定导入指纹，持久重放不因普通 24 小时幂等清理而失效。重复 UID 各建独立 account_id；旧强制登录的固定 UID 不作为已验证身份。源条目改换凭据会形成新导入项，不隐式覆盖既有账号。删除后的相同导入指纹不会使账号重新出现。工作目录/CLI 默认账号的管理仍属 P6。

## 文件读取和查询快照

`GET /accounts/{id}/files` 接受 path、page（默认 1）、page_size（默认 100，最大 1000）、order_by=name|time|size、direction=asc|desc 和 fields。默认路径 `/`；相对路径必须带 base_path。P3 使用显式 account_id 与绝对路径/base_path；context 服务在 P6 开放。

列表返回本页 entries/summary。full page 的 has_more 和 total 为 null，短页/空页可确定 has_more=false；不凭页长声称拿到了全目录。fields 默认 all，基础 fs_id/path/name/is_dir/size_bytes/md5_status 始终返回；summary 只统计当前页。Pan 未提供的 app_id、子目录标志和未知时间保持 null。

```http
GET /api/v1/accounts/acct_example/files?path=/data&page=2&page_size=1000&order_by=time&direction=desc
GET /api/v1/accounts/acct_example/files/metadata?path=report.txt&base_path=/data
POST /api/v1/accounts/acct_example/files/metadata
Content-Type: application/json

{"paths":["/data/report.txt","/missing"]}
```

批量元数据保留每个输入的成功/错误和完整计数。paths 为字面量，通配符单独提交到 `POST .../path-matches`；匹配按路径分段使用 CLI 相同的 path.Match 语法，分页遍历最多 10000 个候选节点，逐 pattern 返回结果或错误。

搜索使用 `GET .../files/search?path=/data&keyword=report&page_size=100`；后续请求携带同样参数与 cursor。首次返回上游实际收到集合的加密快照，后续不重复搜索。查询/调用方/账号/凭据版本或页大小变化返回 409；过期 410，损坏游标 400。搜索/任务列表快照最多 10000 项、TTL 600 秒；任务查询在计数前检查原权限，时间过滤保留RFC3339Nano精度；单次任务查询载荷最多32MiB，全局最多128个活跃快照、128MiB加密载荷。超过条目上限返回 413，搜索可改用扫描。文件同步响应读取上限 32 MiB，异常上游数据不能产生无界内存分配。

## 文件扫描与任务

```http
POST /api/v1/accounts/acct_example/file-scans
Idempotency-Key: scan-example
Content-Type: application/json

{"paths":["/data"],"options":{"mode":"tree","depth":1,"include_ids":true,"max_nodes":"100000","max_duration_seconds":3600}}
```

返回 202 job。mode 支持 tree、recursive、statistics；depth=-1 不限深度，0 包含选择根和其直接子项，与 CLI 的目录展开起点一致。max_nodes 默认 100000、部署硬上限 1000000；最长 86400 秒。达到节点/时长上限返回 complete=false 和具体 truncation_reason；单项或目录分页错误保留已知结果并继续其他选择根。存在成功和失败时为 partially_succeeded，全部失败为 failed。

扫描共用 P2 唯一持久派发器，仍为 2 执行、16 等待，不另建后台队列。JSONL artifact 流式写入，不在内存堆积全部输出；tree 行额外有 depth，include_ids=false 不写 fs_id。statistics 输出统计记录，逐项事实仍可经 JOB03 获取。

- `GET /jobs`：按账号、kind、state、created_after/before 过滤，绑定调用方和原账号/root/业务权限的游标快照。
- `GET /jobs/{id}`：有效选项、版本、状态、阶段、进度、实际可用控制；未知总量为 null。
- `GET .../items`：逐项结果、错误和脱敏检查点摘要。扫描使用不可变条目的固定高水位游标，可读取超过 10000 项的大任务；TTL 600 秒，后续新增项不混入原游标。重新不带 cursor 请求可观察新进度。
- `GET .../result`：只在终态返回，未完成为 409。部分成功和取消保留实际已完成范围及 artifact_ids。
- `POST .../cancel`：接受控制意图，不承诺远端回滚。重复控制和幂等重放沿用 P2。
- `POST .../retry`：默认 scope=failed，可选 failed_and_cancelled；只选择相应项创建新 job，返回 retry_of_job_id。每次最多 10000 个重试项目，超限 413；没有符合项为 409；扫描因准备/结果文件等任务级错误失败且无失败逐项记录时，原选择根作为失败请求重试。新任务使用当前配置/凭据，重新准备目标，沿用原显式请求选项，不携带原 uploadid/下载临时文件。

传输的 resume 延续原任务、使用原配置/凭据和检查点。扫描重启后 running/cancelling 保留 interrupted/已知取消结果；可取消后 retry 新建读取任务，JOB08 不声称支持扫描续读。未知提交结果仍由原 P2 核验路径处理。

## SSE

`GET /jobs/{id}/events` 首先返回一致读取的 job.snapshot 与事件高水位，再跟随后续提交。Last-Event-ID 形如 `evt_job_example_17`，只回放其后的持久事件；跨任务/格式错误 400、未来 409、超过保留窗口 410。状态、检查点、逐项结果、控制重放和事件在相应 SQLite 事务内提交，序号不因清理或重启复用。

事件包括 job.created、job.state_changed、job.progress、item.completed、job.finished。每任务最多保留 10000 个或 86400 秒，以先达到者为准；15 秒无 ID 注释心跳。全局最多 64 个连接，每连接最多读取/保留 128 帧，追踪落后超过上限即断开；客户端带最后处理的 ID 重连。写入空闲期限 30 秒；慢客户端不阻塞调度器或持久事件写入。终态事件提交并发送后可结束连接，客户端自行去重。

## Artifact 和本地根目录

`GET /local/roots` 仅返回允许的 root_id、标签、读写状态和可确认的剩余空间，不返回服务器绝对目录。Linux/macOS 查询可用空间，其他平台无法确认时为 null。

`GET /jobs/{id}/artifacts` 和 `GET /artifacts/{id}` 返回结果文件元数据；`GET|HEAD .../content` 需要原任务 owner/admin、原账号及业务/root 权限。文件下载使用 application/octet-stream，原媒体类型保留于元数据；不依赖随机 ID 作为授权。

- 支持单个 bytes Range 和 If-Range。语法错误/多范围 400，不满足 416（Content-Range: bytes */size），If-Range 不匹配返回完整 200；HEAD 所有状态无 body，错误业务码在 X-Error-Code。
- 完成结果文件后保留 24 小时。每文件最多 256 MiB，全局最多 1 GiB 和 128 个未删除文件；写入预留最大片额，失败不发布可用结果 ID。
- 活动任务和已打开响应持有引用。DELETE 遇到引用返回 409；到期不会物理删除被引用文件，过期读取返回 410。释放引用后每分钟清理；删除保留 tombstone，任务摘要不随结果文件删除。
- 文件在独立 0700 目录、以随机 ID 和 0600 权限写入；Unix 复用目录描述符/O_NOFOLLOW 打开，不接受客户端文件路径。完整写入、同步并保存元数据后才发布。数据库、master.key 和 artifacts 目录应配对备份；schema v4 不支持用 P2 二进制直接降级打开。

P2 的真实百度证据仍只适用于原账号、时间和专用目录；本阶段上游验证状态在[P3 验收](http-api-p3-verification.md)单独列出。
