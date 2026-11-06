# HTTP API P0 核对与行为基线

本文保留P0交付时的历史结论；当前P1状态见[P1验证记录](http-api-p1-verification.md)。

日期：2026-11-02。源码基线：`1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73`；进入本轮时工作分支 HEAD 为仅增加设计文档的 `bf76d65b2f37e0e4460d836ca75b76246f4d06dc`。HTTP 契约版本为 `0.1.0-p0`。本轮只交付设计、契约、示例、源码清单和校验/探测资料，不修改 `main.go`、业务包、根 `go.mod/go.sum` 或旧 CLI 数据。

## 交付与验证结论

| 项目 | 结果 | 证据 |
| --- | --- | --- |
| 路由目录与契约 | 105/105 operation，89 path，190 schema，operationId/方法/路径/阶段逐项一致 | [OpenAPI](../api/openapi/openapi.yaml)、[校验入口](../api/openapi/tools/check_contract.py) |
| 请求响应示例 | 105 组完整 HTTP exchanges；51 个有结构化 body 的操作含请求示例，89 个操作含成功 JSON 示例；其余为无 body、流或明确历史拒绝 | [全部 exchanges](../api/openapi/examples/http-exchanges.json)、[常用请求](../api/openapi/examples/requests.http) |
| Schema 边界 | 67 个正反例通过；内嵌参数/请求/响应与 SSE 帧一起校验 | [schema-cases](../api/openapi/examples/schema-cases.json) |
| SSE | 3 个实际数据帧与 JSON schema、event/id 一致；心跳为注释 | [job-events.sse](../api/openapi/examples/job-events.sse) |
| 源码逐项映射 | 609/609 条 AST 记录有处理分类；当前源码哈希、清单和人工表一致 | [source-inventory](../api/openapi/baselines/p0-source-inventory.json)、[人工矩阵](http-api-coverage.md) |
| 现有离线测试 | Go 1.23.12 与 Go 1.26.2 下各 14 个包通过；另确认 3 项已有失败，见下文 | 本文测试基线与复现命令 |
| 存储可用性 | 隔离探测通过原子事务、回滚、关闭重开和 integrity_check；5 个初始服务目标可 CGO=0 编译 | [可复现探测](../api/openapi/tools/probe_sqlite.py) |
| HTTP 功能实现 | 0/105，本轮不实现服务 | 每个 operation 的 x-capability.implementation=planned |
| HTTP 行为验收 | 尚未执行，25 组已编制 | [behavior-cases](../api/openapi/examples/behavior-cases.json) |
| 百度上游实测 | 未运行，无账号/文件操作 | capability 的 availability 与 verified_at 单独记录 |

这里的覆盖率只统计明确盘点范围内的源码归属与目标契约。离线 schema 通过、现有 CLI 单元测试通过、服务 HTTP 行为通过及真实百度可用性分别记录。

## 遗漏与不一致的处理

| 编号 | 发现与源码依据 | P0 决定及后续阶段 |
| --- | --- | --- |
| P0-01 | 原有文件、转存、导出示例中本地 path 与 relative_path、顶层选项与 options 混用 | 本地文件统一 relative_path，目标目录 directory；执行选项统一 options；同步修订三份文档和可校验 schema |
| P0-02 | 设计写“全部批量写入进入 job”，OFF01 却同步返回百度 ID，OFF06 同时允许同步总数和 job | OFF01/OFF06 固定 202 job，百度 ID/处理数量在 JOB04；P5 实现 |
| P0-03 | [cloud_dl.go:149](../baidupcs/cloud_dl.go:149) 对超过 100 个 ID 静默截断，且跳过没有返回的 ID | OFF03 最多 100 个唯一 ID，超限 413；每个输入均有成功/失败项；不把上游缺项当完整成功；P5 实现 |
| P0-04 | [prepare.go:692](../baidupcs/prepare.go:692) 离线列表固定 start=0/limit=1000；原设计容易被理解为服务游标能获取全部任务 | 快照只覆盖实际返回集合，显式记录上游范围限制；将来全量分页需独立补上游适配 |
| P0-05 | 下载票据只有创建路由，没有确定兑换入口或身份语义 | DWN03/04 可用 Bearer 或短期 ticket；只绑定指定 owner/account/path，复核权限；P4 实现，不新增任意代理入口 |
| P0-06 | “统一 Problem body”与 HEAD 无 body 冲突；初稿没有 415/428，也未明确缺少 ETag 的行为 | HEAD 包括错误均无 body，X-Error-Code 表达业务码；补齐 415/428，强 If-Match 缺少 428、不匹配 412 |
| P0-07 | 任务示例累计条目使用 number，且 completed_items 与后文 processed 统计命名不一致 | 累计数量、字节和速率全部字符串；统一 processed_items，succeeded/failed/skipped/cancelled 分列；未知总数 null |
| P0-08 | SSE 首次订阅的“快照”没有事件模型，过期/未来/跨任务游标不明确 | 增加 job.snapshot；注释心跳无 ID；冻结过期 410、未来 409、跨任务 400，回放只取游标之后 |
| P0-09 | TRF01 只描述解析结果，未说明多页分享和嵌套目录选择 | inspection_id 绑定账号/调用方/凭据版本，支持目录展开与 opaque cursor；TRF02 默认全部根项须解析完整，不能只转存第一页；P5 实现 |
| P0-10 | [main.go:1400](../main.go:1400) 明确支持任意有效分享天数；不能按常见值把 period_days 限成 1/7/30 | 允许 0 至有界正整数，0 永久；上游拒绝转 422；增加 3 天分享正例 |
| P0-11 | [share.go:47](../baidupcs/share.go:47) 的 ShareSURLInfo 只有 Pwd/ShortURL | SHR03 只保证已知短链接/提取码字段；未知列表元数据省略，不能凭空填 0 或伪造状态 |
| P0-12 | [recycle.go:10](../baidupcs/recycle.go:10) 的 LeftTime 未说明单位；账号 Sex/Age 也不能静默丢失 | 回收站保留 upstream_left_time，未验证不推算 expires_at；账号保留可空 sex/age，年龄原值不猜单位 |
| P0-13 | 覆盖矩阵遗漏 [OrderOptions](../baidupcs/file_directory.go:93) 两个字段，NoSplitFile/ListTask 的表述缺少明确路由归属 | 补 By→FIL01 order_by、Order→direction；45 个选项字段进入 AST 检查；NoSplitFile→UPL01，ListTask→JOB/EXP01 |
| P0-14 | 原配置表支持 22 个客户端字段，设计却又允许 PATCH 写监听/TLS/存储等启动参数 | v1 CFG02 只写 22 个客户端字段；启动字段通过固定服务配置和重启生效，reload 遇到此类变更返回 422；不增加未定义的写入口 |
| P0-15 | CLI SaveDir 默认路径未经管理员 root 授权；[requester/dial.go:70](../requester/dial.go:70) 代理规则为字符串子串，且可能隐式继承进程代理环境 | HTTP savedir 未绑定授权 root 时可空；PATCH 只收受控目录；网络实例显式版本化，代理精确主机/通配子域匹配，不继承旧子串规则或隐式环境代理；P1 适配 |
| P0-16 | skip/overwrite/rsync、test/dry_run、retry=0、显式 false、Rname/NoSplitFile 很容易在“字段都接收”时被误报可用 | 固定有效选项和错误行为；rsync 只比较大小，test 实际传输，dry_run 不执行；历史选项 true 为 422，INT06 默认 503；能力不伪报 available |
| P0-17 | 单模块、纯 Go 交叉编译目标与 SQLite 的平台支持并非自动相容 | 初期 server 支持五个平台组合；CLI 继续保留原构建目标；P1 隔离 server 入口/存储依赖的构建条件，不能把驱动无条件导入 CLI |
| P0-18 | [export.go:66](../internal/pcscommand/export.go:66) 的空 RootPath 保留原路径，非空允许相对值；复用可缺省请求 schema 又会允许空的有效选项快照 | 增加 ExportRootPath，保留空串/相对虚拟根；为生效选项增加独立 Effective* schema，上传下载等快照缺少字段校验失败；67 个边界用例覆盖这些差异 |
| P0-19 | [cp_mv_rename.go:26](../baidupcs/cp_mv_rename.go:26) 的 cpmvOp 将准备错误赋给 err，随后裸 return 会返回尚未赋值的 pcsError(nil) | 静态核对发现的已有错误路径，不触发百度调用；FIL09—11 不能直接把 nil 当成功；P5 先修复准备错误传递并做受控失败验收 |
| P0-20 | [maniper.go:118](../internal/pcsconfig/maniper.go:118) 的 SetupUserByBDUSS 用正则匹配后直接访问 sub[1]；BDUSS 字段与原始 COOKIES 也存在双份状态 | P2 在调用旧适配前完整解析 Cookie，错误格式返回 400；采用唯一规范化 Jar，明确 cookies=null 与单字段吊销的同步语义，防止清除失效 |

以上均已落到目标契约或实施约束；与原有业务实现的差异会在对应实施阶段处理。没有把当前实现缺陷通过修改生产代码“修成符合契约”。

## 源码与行为基线

AST 盘点只读取活动 Go 声明，排除测试及块注释，按 receiver 区分同名方法。命令别名、参数别名不重复算能力。`upload2.go` 主体注释排除；未注册成命令的 RunFixMD5 仍覆盖；未接入的 SuperTransfer 归实验内部实现。

| 范围 | 数量 |
| --- | --- |
| CLI 顶层 / 全部节点 / 叶子 / 命令组 | 34 / 51 / 46 / 5 |
| 命令参数 / 全局参数 | 86 / 1 |
| baidupcs 顶层导出函数和方法 | 132 |
| pcscommand / pcsconfig 导出函数和方法 | 34 / 44 |
| pcsupload / pcsdownload 导出函数和方法 | 23 / 14 |
| taskframework / uploader / downloader 导出函数和方法 | 16 / 40 / 99 |
| PCSConfig 导出字段 | 24，2 个账号相关字段分域，22 个客户端配置 |
| 业务选项字段 | 45，包含新增核对的 OrderOptions 两个字段 |

| 行为 | 源码观察及服务化时要保持/明确改变的部分 |
| --- | --- |
| 下载默认目录与层级 | Config.InitDefaultConfig 根据 OS/HOME 选 Downloads；Baidu.GetSavePath 还加账号目录；RunDownload 使用 PreBase + basename 或完整网盘路径。HTTP 改为显式 root，并保留选择根层级，多根同名冲突不得静默覆盖 |
| 上传目录与空文件 | RunUpload 遍历本地源并保留相对于源父目录的层级；空文件仍是文件。服务使用明确源引用，不能只保留 basename |
| rsync 与跳过 | 现有策略按目标大小判断；正常跳过是 skipped，不是失败，也不能计入实际上行字节 |
| 重试次数 | RunUpload/RunDownload 仅对负数使用默认重试；0 是不重试；HTTP 拒绝负数，省略才用默认 3 |
| 默认布尔值 | 当前 RunDownload 会用全局 NoCheck 覆盖 false，RunUpload 无条件读取 IgnoreIllegal。HTTP 必须按字段 presence 解析，显式值优先；这是已决定的服务适配行为 |
| 断点恢复 | 上传数据库、uploadid、文件摘要与全局停止均不能直接作为多账号任务的隔离边界；norapid 不要求先补完整摘要；P4 验证源指纹与按任务控制 |
| 转存 | 当前支持整段文本中提取规范 URL、URL pwd、显式提取码、fs_id 选择和 collect；当前 CLI 对普通文本中的独立“提取码:”并没有完整结构化解析保证。服务适配器补齐解析与优先级，并测试失败情况 |
| 转存后下载 | 当前等待固定 10 秒；服务采用有上限的可见性检查和持久化下载子任务 |
| 分享参数 | 当前异常长度提取码会被核心重新生成；HTTP 显式非法值拒绝，省略才生成；period 保留任意有效天数 |
| 导出 | 当前为追加文件、root 改写、recursive/retry、legacy command/link 和 stdout；HTTP 默认独立 artifact，部分失败留明细；不承诺旧秒传格式可重新导入 |
| 校验 | 长度、完整 MD5、256 KiB slice MD5、CRC32 和 block MD5 分开；远端缺头、目录或分片摘要不等于 verified；代码 MaxUploadSize 为 137438953472 字节，旧 20GB 文案不是限制依据 |

这些是源码观察和未来验收基线；未使用用户网盘数据证明上传、下载或转存成功。

## 现有测试分类与结果

本机 darwin/arm64。对明确不访问百度和其他外网的测试，先用系统 Go 1.26.2 运行，再用项目声明的 Go 1.23 系列（1.23.12）确认。以下命令两次均 14 个包通过：

```sh
GOTOOLCHAIN=go1.23.12 go test -count=1 -timeout=90s \
  ./pcsliner/args ./pcsutil/cachepool ./pcsutil/converter \
  ./pcsutil/escaper ./pcsutil/waitgroup ./pcsutil/taskframework \
  ./baidupcs/netdisksign ./baidupcs/expires/cachemap \
  ./requester ./requester/rio ./requester/rio/speeds \
  ./requester/uploader ./internal/pcscommand \
  ./internal/pcsfunctions/pcsupload
```

另运行/分类的现有问题：

| 用例/包 | 实际结果 | 处理 |
| --- | --- | --- |
| requester/downloader 的本地 TestRandomNumber / TestRangeListGen | 整个测试包编译失败：`download_test.go:44: d.monitor.ShowWorkers undefined` | 即便 -run 只选离线用例也编译整个测试包；记录为源码基线失败，P4 修复/迁移时先处理 |
| pcsutil.TestWalkDir | 失败：`lstat /Users/syy/tmp: no such file or directory` | 开发者固定本地路径；本轮没有创建假目录让测试通过，后续迁移为受控 fixture |
| pcsutil/checksum.TestChecksum | 失败：固定路径 `/Users/syy/go/src/github.com/qjfoidnh/BaiduPCS-Go/BaiduPCS-Go` 不存在 | 用例前半段可读本包 file.go，但不能因此把整个用例记为通过 |
| pcsutil/getip.TestGetIP | 未运行 | 真实外网出口查询 |
| requester/downloader.TestExample / TestDownloadTIM | 未运行传输 | 会访问外网并写文件；所属测试包另有上述编译失败 |
| 所有真实百度集成与新 HTTP 行为规格 | 未运行 | 后续阶段使用专用测试账号和目录；不混入 P0 schema/现有离线测试结论 |

没有运行不加筛选的 `go test ./...`，避免触发旧网络下载和对本地环境的隐含假设。只列已实际运行的成功与失败，不宣称仓库全部测试通过。

## SQLite 与平台探测

固定探测候选 `modernc.org/sqlite v1.38.2`，其模块声明 Go 1.23.0；本机以 `GOTOOLCHAIN=go1.23.12 CGO_ENABLED=0` 运行，返回 SQLite **3.50.3**。探测在系统临时目录创建独立 module、临时数据库和构建产物，结束后清理；项目根依赖文件未改变。

```sh
python3 api/openapi/tools/probe_sqlite.py --cross
```

[探测源模板](../api/openapi/tools/sqlite-probe.go.txt) 包括 WAL 设置、jobs/events/idempotency 同事务提交、回滚不可见、关闭后重新打开和 integrity_check。它只证明驱动可用于本项目要求的基础存储，不替代 P1/P3 的独占锁、迁移、崩溃恢复、并发、配额和性能验收。

| 目标 | Go 1.23.12、CGO=0 编译 | 运行证据 |
| --- | --- | --- |
| darwin/arm64 | 通过 | 本机事务、回滚、重开和完整性检查通过 |
| darwin/amd64 | 通过 | 仅交叉编译，未在目标机运行 |
| linux/amd64 | 通过 | 仅交叉编译，未在目标机运行 |
| linux/arm64 | 通过 | 仅交叉编译，未在目标机运行 |
| windows/amd64 | 通过 | 仅交叉编译，未在目标机运行 |
| linux/mipsle | 不通过，符合已知不支持边界 | modernc/libc 的 errno/fcntl 等包没有可用构建文件；不能发布此目标的 server |

初始 server 验收矩阵采用前五项，CLI 原构建矩阵保留。其他目标未在本轮声称支持；P1 必须明确构建条件，使不支持此驱动的平台仍可编译 CLI，而不是缩小 CLI 支持范围。驱动版本与依赖将仅在 P1 引入服务存储时进入主模块，本轮未加入 SQLite 生产适配器。

协议与工具依据：[OpenAPI 3.1.1](https://spec.openapis.org/oas/v3.1.1.html)、[校验器文档](https://openapi-spec-validator.readthedocs.io/en/latest/)、[SQLite 驱动官方仓库及 v1.38.2 源码](https://gitlab.com/cznic/sqlite/-/tree/v1.38.2)。本次兼容性结论来自上述实际版本的本地探测，不由“最新版本”文案推导。

## P1 交接条件

P0 的契约、示例、源码归属、离线行为基线和工具/存储兼容性资料已齐备。P1 从配置快照、独立 ClientFactory、鉴权、数据目录锁和上述五平台构建条件开始；P0 的 planned 不提前改成 implemented。现有测试中的固定路径及下载器测试编译错误保持单列，在对应能力回归前修复或迁移。25 组行为规格由后续阶段逐项转为实际验收，不沿用“schema 通过”作为业务可用证明。
