# BaiduPCS Go HTTP API 源码覆盖矩阵

日期：2026 年 11 月 5 日。基线提交：`1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73`。原有业务源码链接固定到该提交；P1新增server入口使用本地源码链接。机器清单随实现更新，P0冻结版本保存在api/openapi/baselines/p0-source-inventory.json。

本文中的路由 ID 对应[HTTP API 设计方案](http-api-design.md)，阶段和验收对应[实施计划](http-api-implementation-plan.md)。此表记录目标映射，尚不代表相关接口已经实现或完成上游验证。实施顺序已调整为P2先验证账号/实际上传下载/取消/重启恢复，context和CLI默认值等管理能力后置；目标映射不要求提前实现所有管理资源。P0 已保存 [机器清单](../api/openapi/source-inventory.json)、[OpenAPI 与示例](../api/openapi/README.md) 和 [核对报告](http-api-p0-baseline.md)；校验器同时检查 AST→本表→路由 ID，发现新增遗漏或过时行会失败。

## 1 盘点范围

| 对象 | 数量 | 核对方式 |
| --- | --- | --- |
| main.go 顶层命令 | 35（P0为34） | Go AST 读取 app.Commands，不把全局 verbose 当命令 |
| 命令及子命令节点 | 52（P0为51） | 包含 5 个命令组和 47 个叶子命令；别名不重复计数 |
| 命令参数 | 90（P0为86） | 每个命令的 Flags 字段，逐项映射 |
| 全局参数 | 1 | verbose，另有框架自动提供的 help/version |
| baidupcs 顶层包导出函数和方法 | 133（P2为132） | Go AST FuncDecl；排除测试和块注释，包含底层 Prepare 与格式辅助 |
| PCSConfig 导出字段 | 24 | 2 个账号相关字段分域，其余 22 个类型化配置 |
| pcscommand 导出函数 | 34 | 包括没有注册成命令的 RunFixMD5 |
| pcsconfig 导出函数和方法 | 44 | 配置生命周期、账号和网络客户端适配 |
| 12 个业务选项结构导出字段 | 45 | 包含 P0 补齐的 OrderOptions 两个字段；逐字段映射 |
| 传输/任务相关导出函数和方法 | 193 | pcsupload 23、pcsdownload 14、taskframework 16、uploader 40、downloader 100 |
| 机器清单合计 | 616（P0为609、P1为614、P2为615） | 404 个导出函数/方法、52 个命令节点、90 个参数、1 个全局参数、24 个配置字段、45 个选项字段 |
| 其他相关实现 | 分组核对 | 传输、调度、校验、错误、签名和本地工具；不把每个工具方法公开为路由 |

清单反映当前代码实际定义，不从 README 的旧命令说明反推可用能力。`fixmd5`、`rapidupload` 没有注册为当前 CLI 命令；`upload2.go` 中的块注释不计入函数数。子包的缓存、签名、JSON、错误处理和平台工具按基础设施归属，不按函数数量制造 HTTP 端点。

## 2 全部 CLI 命令映射

| 命令 | 目标接口或保留方式 | 行为说明 | 源码 |
| --- | --- | --- | --- |
| server | SYS01、SYS02、SYS03、SYS04、SYS08、SYS09、CFG01、CFG02、CFG03、CFG04、CFG05、CFG07 | P1服务入口；配置、网络和认证独立，其他业务按阶段开放 | [main.go:339](../main.go:339) |
| run | CLI 专属 | 执行系统命令；不提供远程命令执行接口 | [main.go:329](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L329) |
| env | SYS03、CFG01 | 只返回本服务运行信息和有效配置，不暴露任意环境变量 | [main.go:352](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L352) |
| update | CLI 专属；SYS03 | 自更新保留在 CLI；HTTP 可查询当前版本 | [main.go:379](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L379) |
| login | ACC01、LOG01 至 LOG06 | 凭据导入、密码与 QR 登录会话；支持状态以 capabilities 为准 | [main.go:399](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L399) |
| su | CTX03、CFG09 | 调用方切换账号与管理 CLI 默认账号分开 | [main.go:477](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L477) |
| logout | ACC05、CTX04 | 删除本地保存账号或仅关闭浏览上下文；不是百度全局注销 | [main.go:551](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L551) |
| loglist | ACC02 | 返回调用方可访问的账号清单 | [main.go:594](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L594) |
| setastoken | ACC06 | 更新 access_token，凭据只写 | [main.go:605](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L605) |
| who | ACC03、CTX02 | 指定账号资料或上下文选中账号 | [main.go:639](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L639) |
| quota | ACC08 | 总量、已用、剩余空间 | [main.go:651](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L651) |
| cd | CTX03、FIL01 | 更新调用方 cwd，可附带目录列表 | [main.go:662](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L662) |
| ls | FIL01 | 分页列表、排序、完整字段 | [main.go:705](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L705) |
| search | FIL04 | 目录、关键字、递归与结果字段 | [main.go:787](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L787) |
| tree | FIL05 | mode=tree 的扫描 job；深度与 fs_id 完整覆盖 | [main.go:838](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L838) |
| pwd | CTX02 | 返回调用方上下文 cwd | [main.go:879](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L879) |
| meta | FIL02、FIL03 | 单个与批量元数据 | [main.go:890](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L890) |
| rm | FIL08 | 删除 job，逐项结果 | [main.go:912](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L912) |
| mkdir | FIL07 | 创建目录 job | [main.go:946](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L946) |
| cp | FIL09 | 明确 from/to 的复制 job | [main.go:962](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L962) |
| mv | FIL10、FIL11 | 移动和重命名 job | [main.go:990](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L990) |
| download | DWN01、JOB 系列 | 本地下载与全部选项，独立任务生命周期 | [main.go:1021](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1021) |
| upload | UPL01、JOB 系列 | 本地和暂存源上传与全部有效选项 | [main.go:1172](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1172) |
| locate | DWN02 | PCS、Pan、直接链接及 UA 要求 | [main.go:1250](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1250) |
| sumfile | INT03 | 本地文件摘要 job；不承诺旧秒传重新可用 | [main.go:1283](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1283) |
| transfer | TRF01、TRF02 | 解析、选择 fs_id、归档和下载子任务 | [main.go:1334](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1334) |
| share | SHR01 至 SHR04 | 命令组；各子命令如下 | [main.go:1384](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1384) |
| share set | SHR01 | 提取码、有效天数和带码链接 | [main.go:1395](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1395) |
| share list | SHR02、SHR03 | 列表与详情，可选补充提取码 | [main.go:1431](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1431) |
| share cancel | SHR04 | 批量取消 job | [main.go:1448](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1448) |
| export | EXP01、ART02 | 结构化或兼容格式导出；结果文件下载 | [main.go:1465](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1465) |
| offlinedl | OFF01 至 OFF07 | 百度离线任务命令组 | [main.go:1536](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1536) |
| offlinedl add | OFF01 | 批量 URL 和保存目录 | [main.go:1563](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1563) |
| offlinedl query | OFF03、OFF07 | 按百度离线任务 ID 查询 | [main.go:1584](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1584) |
| offlinedl list | OFF02 | 返回百度离线任务 | [main.go:1606](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1606) |
| offlinedl cancel | OFF04 | 批量取消百度任务 | [main.go:1616](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1616) |
| offlinedl delete | OFF05、OFF06 | 逐项删除或全部清空 | [main.go:1638](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1638) |
| recycle | REC01 至 REC04 | 回收站命令组 | [main.go:1675](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1675) |
| recycle list | REC01 | 上游分页列表 | [main.go:1701](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1701) |
| recycle restore | REC02 | 恢复，保存实际成功 ID | [main.go:1718](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1718) |
| recycle delete | REC03、REC04 | 永久删除或清空 | [main.go:1733](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1733) |
| config | CFG01、CFG03、CFG07 | 完整有效配置、schema 和默认值 | [main.go:1762](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1762) |
| config set | CFG02、CFG04 | 类型化部分更新和预先校验 | [main.go:1775](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1775) |
| config reset | CFG05 | 仅重置客户端字段，保留凭据和任务 | [main.go:1981](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L1981) |
| match | FIL06 | 显式通配符解析 | [main.go:2000](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L2000) |
| tool | SYS03、SYS05、LOC02、LOC03 | 工具命令组，业务逐项映射 | [main.go:2024](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L2024) |
| tool showtime | SYS03 | UTC 时间；调用方可转换北京时间 | [main.go:2032](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L2032) |
| tool getip | SYS05 | 受权的内外网诊断 | [main.go:2040](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L2040) |
| tool enc | LOC02 | 兼容加密 job；默认保留源文件 | [main.go:2060](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L2060) |
| tool dec | LOC03 | 兼容解密 job；默认保留源文件 | [main.go:2100](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L2100) |
| clear | CLI 专属 | 清空终端屏幕，无网络业务 | [main.go:2142](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L2142) |
| quit | CLI 专属 | 退出当前 CLI；服务由进程管理器和信号关闭 | [main.go:2154](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/main.go#L2154) |

## 3 全部命令参数映射

| 命令 | 参数 | HTTP 等价表达或处理 |
| --- | --- | --- |
| server | -listen | CLI 专属启动参数；默认127.0.0.1:5299，不经CFG02修改 |
| server | -data-dir | CLI 专属启动参数；独立服务数据目录，不读取旧CLI配置 |
| server | -config | CLI 专属启动参数；固定服务JSON配置，禁止请求切换来源 |
| server | -token-file | CLI 专属启动参数；管理员客户端token输入文件，鉴权仅保存摘要 |
| update | -y | CLI 交互确认；不作为服务自更新接口 |
| login | -username | LOG01 username |
| login | -password | LOG01 password，短期秘密 |
| login | -bduss | ACC01 credentials.bduss |
| login | -ptoken | ACC01 credentials.ptoken；按原项目保留可选输入 |
| login | -stoken | ACC01 credentials.stoken |
| login | -cookies | ACC01 credentials.cookies，完整 Cookie 解析 |
| logout | -y | ACC05 明确 DELETE 已表达删除意图；无终端交互确认 |
| cd | -l | CTX03 include_listing |
| ls | -l | FIL01 fields=all 和当前页 summary；全量统计走 FIL05 |
| ls | -asc | FIL01 direction=asc |
| ls | -desc | FIL01 direction=desc |
| ls | -time | FIL01 order_by=time |
| ls | -name | FIL01 order_by=name |
| ls | -size | FIL01 order_by=size |
| search | -l | FIL04 fields=all 和当前页 summary |
| search | -r | FIL04 recursive |
| search | -path | FIL04 path；相对路径必须绑定 context 或 base_path |
| tree | -depth | FIL05 options.depth，-1 不限深度但有资源上限 |
| tree | -fsid | FIL05 options.include_ids；完整 FileEntry 自带 fs_id |
| download | -test | DWN01 options.test；实际测试传输但不落最终文件 |
| download | -ow | DWN01 options.overwrite |
| download | -status | DWN01 options.report_workers；JOB03/JOB05 结构化 worker 状态 |
| download | -save | DWN01 destination 显式指向允许的本地根目录；不采用服务进程 cwd |
| download | -saveto | DWN01 destination.root_id + directory |
| download | -x | DWN01 options.executable；Windows 不生效并返回有效选项 |
| download | -mode | DWN01 options.mode=locate\|pcs\|stream |
| download | -p | DWN01 options.parallel，总连接预算 |
| download | -l | DWN01 options.file_concurrency |
| download | -retry | DWN01 options.max_retries；0 不重试 |
| download | -nocheck | DWN01 options.verify_checksum 的反向映射 |
| download | -mtime | DWN01 options.preserve_mtime |
| download | -dindex | DWN01 options.link_index，0 起点 |
| download | -fullpath | DWN01 options.path_mode=full；默认 relative |
| upload | -p | UPL01 options.parallel_per_file |
| upload | -retry | UPL01 options.max_retries；0 不重试 |
| upload | -l | UPL01 options.file_concurrency |
| upload | -norapid | UPL01 options.skip_rapid_check；保留无完整摘要的恢复路径 |
| upload | -policy | UPL01 options.policy=skip\|overwrite\|rsync，rsync 按当前大小比较语义 |
| locate | -pan | DWN02 source=pan；默认 source=pcs |
| transfer | -download | TRF02 options.download_after；生成有依赖的下载子 job |
| transfer | -collect | TRF02 options.collect；归档到同名目录 |
| transfer | -rname | TRF02 options.randomize_name；当前实际调用已注释，true 返回 422 |
| transfer | -fs_id | TRF02 fs_ids 字符串数组，兼容主项目指定单个或多个 ID 的目的 |
| share set | -p | SHR01 password；缺省按核心规则生成 |
| share set | -period | SHR01 period_days；0 永久 |
| share set | -f | SHR01 combined_link；仅控制带码链接表达 |
| share list | -page | SHR02 page，从 1 开始 |
| export | -root | EXP01 options.root_path，导出路径改写 |
| export | -out | EXP01 结果 artifact；可选 options.destination 替代任意输出文件路径 |
| export | -retry | EXP01 options.max_retries |
| export | -r | EXP01 options.recursive |
| export | -link | EXP01 options.format=legacy_link；明确丢失目录信息 |
| export | -stdout | EXP01 结果内容通过 ART02 返回，不抓取终端输出 |
| offlinedl add | -path | OFF01 save_path，明确网盘绝对路径 |
| offlinedl delete | -all | OFF06 清空操作；不通过空 ID 列表隐式清空 |
| recycle list | -page | REC01 page，从 1 开始 |
| recycle delete | -all | REC04 清空操作；不通过空 ID 列表隐式清空 |
| config set | -appid | CFG02 appid；有 presence、范围和生效方式校验 |
| config set | -cache_size | CFG02 cache_size；有 presence、范围和生效方式校验 |
| config set | -max_parallel | CFG02 max_parallel；有 presence、范围和生效方式校验 |
| config set | -max_upload_parallel | CFG02 max_upload_parallel；有 presence、范围和生效方式校验 |
| config set | -max_download_load | CFG02 max_download_load；有 presence、范围和生效方式校验 |
| config set | -max_upload_load | CFG02 max_upload_load；有 presence、范围和生效方式校验 |
| config set | -max_download_rate | CFG02 max_download_rate；有 presence、范围和生效方式校验 |
| config set | -max_upload_rate | CFG02 max_upload_rate；有 presence、范围和生效方式校验 |
| config set | -savedir | CFG02 savedir；有 presence、范围和生效方式校验 |
| config set | -enable_https | CFG02 enable_https；有 presence、范围和生效方式校验 |
| config set | -ignore_illegal | CFG02 ignore_illegal；有 presence、范围和生效方式校验 |
| config set | -force_login_username | CFG02 force_login_username；有 presence、范围和生效方式校验 |
| config set | -no_check | CFG02 no_check；有 presence、范围和生效方式校验 |
| config set | -upload_policy | CFG02 upload_policy；有 presence、范围和生效方式校验 |
| config set | -user_agent | CFG02 user_agent；有 presence、范围和生效方式校验 |
| config set | -pcs_ua | CFG02 pcs_ua；有 presence、范围和生效方式校验 |
| config set | -pcs_addr | CFG02 pcs_addr；有 presence、范围和生效方式校验 |
| config set | -fix_pcs_addr | CFG02 fix_pcs_addr；有 presence、范围和生效方式校验 |
| config set | -pan_ua | CFG02 pan_ua；有 presence、范围和生效方式校验 |
| config set | -proxy | CFG02 proxy；有 presence、范围和生效方式校验 |
| config set | -proxy_hostnames | CFG02 proxy_hostnames；有 presence、范围和生效方式校验 |
| config set | -local_addrs | CFG02 local_addrs；有 presence、范围和生效方式校验 |
| tool enc | -method | LOC02 options.method，覆盖九种现有算法 |
| tool enc | -key | LOC02 写入临时秘密；API 要求显式提供，不沿用 CLI 默认密钥 |
| tool enc | -disable-gzip | LOC02 options.gzip 的反向映射，默认启用 |
| tool dec | -method | LOC03 options.method，覆盖九种现有算法 |
| tool dec | -key | LOC03 写入临时秘密；API 要求显式提供，不沿用 CLI 默认密钥 |
| tool dec | -disable-gzip | LOC03 options.gzip 的反向映射，默认启用 |

| 全局行为 | 处理 |
| --- | --- |
| --verbose / BAIDUPCS_GO_VERBOSE | 作为服务启动期日志设置；不把任意调试输出和秘密通过接口返回 |
| BAIDUPCS_GO_CONFIG_DIR | 启动时解析；配置导入和数据目录由 bootstrap 管理，不能随请求修改进程环境 |
| 自动 help / --version | OpenAPI 文档和 SYS03；保留 CLI 帮助 |
| 空命令进入交互式 shell、历史命令和补全 | CLI 专属；文件浏览和匹配由 FIL01、FIL06、CTX 系列表达 |

## 4 全部配置字段映射

| 源码字段 | HTTP 归属 | 校验和语义 |
| --- | --- | --- |
| BaiduActiveUID | CFG08、CFG09 | CLI 默认账号，服务使用独立 account_id；不决定请求账号 |
| BaiduUserList | ACC 系列 | 账号仓库和秘密存储；不作为普通配置数组回显 |
| AppID | CFG01、CFG02 appid | 正整数；新版本客户端生效 |
| CacheSize | CFG01、CFG02 cache_size | 字节数，至少 1024；传输提交时固定 |
| MaxParallel | CFG01、CFG02 max_parallel | 下载总连接预算，至少 1，并受服务配额限制 |
| MaxUploadParallel | CFG01、CFG02 max_upload_parallel | 单文件上传并发，至少 1 |
| MaxDownloadLoad | CFG01、CFG02 max_download_load | 同时下载文件数，至少 1 |
| MaxUploadLoad | CFG01、CFG02 max_upload_load | 同时上传文件数，至少 1 |
| MaxDownloadRate | CFG01、CFG02 max_download_rate | 字节每秒，0 不限速；大整数用字符串 |
| MaxUploadRate | CFG01、CFG02 max_upload_rate | 字节每秒，0 不限速；大整数用字符串 |
| UserAgent | CFG01、CFG02 user_agent | 有效请求头文本；按客户端版本生效 |
| PCSUA | CFG01、CFG02 pcs_ua | PCS 请求 UA；不允许换行注入 |
| PCSAddr | CFG01、CFG02 pcs_addr | 受校验的 PCS 主机；不接受任意代理 URL |
| PanUA | CFG01、CFG02 pan_ua | Pan 请求 UA；按客户端版本生效 |
| SaveDir | CFG01、CFG02 savedir（root_id + directory）；LOC01 | 映射允许的本地根目录；任务有效目标明确返回 |
| EnableHTTPS | CFG01、CFG02 enable_https | presence-aware bool；绑定对应客户端实例 |
| FixPCSAddr | CFG01、CFG02 fix_pcs_addr | 固定上传节点策略；按新实例生效 |
| ForceLogin | CFG01、CFG02 force_login_username；ACC01 | 后备身份明确标记未验证，不能按固定 UID 合并账号 |
| Proxy | CFG01、CFG02 proxy | 写入 URL，响应脱敏；空值清除；秘密使用独立引用 |
| ProxyHostnames | CFG01、CFG02 proxy_hostnames | 明确域名匹配规则；导入兼容旧字符串 |
| LocalAddrs | CFG01、CFG02 local_addrs | 有效本地 IP 列表；旧逗号串在适配器转换 |
| NoCheck | CFG01、CFG02 no_check；DWN01 verify_checksum | 默认继承，任务显式选项可覆盖；避免原 RunDownload 默认值吞显式校验 |
| IgnoreIllegal | CFG01、CFG02 ignore_illegal；UPL01 check_filename | 旧配置反向映射为文件名检查；HTTP 本地路径边界仍始终校验 |
| UPolicy | CFG01、CFG02 upload_policy | skip、overwrite、rsync；导入旧 u_policy 字段 |

配置采用 snapshot 和 revision。缓存字节、大小和速率保留二进制单位的旧格式解析兼容，HTTP 返回规范化十进制字节字符串；并发、重试和页码仍用有界整数。完整 Cookie、代理密码和密钥不会作为普通配置明文读取。

## 5 业务选项结构全部字段

### OrderOptions

[baidupcs/file_directory.go:93](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/file_directory.go#L93)

| 字段 | 类型 | HTTP 或内部表达 |
| --- | --- | --- |
| By | `OrderBy` | FIL01 order_by；name、time、size |
| Order | `Order` | FIL01 direction；asc、desc |

### ShareOption

[baidupcs/share.go:12](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/share.go#L12)

| 字段 | 类型 | HTTP 或内部表达 |
| --- | --- | --- |
| Password | `string` | SHR01 password |
| Period | `int` | SHR01 period_days |
| IsCombined | `bool` | SHR01 combined_link |

### TransferOption

[baidupcs/transfer.go:20](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/transfer.go#L20)

| 字段 | 类型 | HTTP 或内部表达 |
| --- | --- | --- |
| Download | `bool` | TRF02 options.download_after |
| Collect | `bool` | TRF02 options.collect |
| Rname | `bool` | TRF02 options.randomize_name；当前 true 不支持 |
| FsID | `string` | TRF02 fs_ids |

### DownloadOptions

[internal/pcscommand/download.go:22](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/download.go#L22)

| 字段 | 类型 | HTTP 或内部表达 |
| --- | --- | --- |
| IsTest | `bool` | DWN01 options.test |
| IsPrintStatus | `bool` | DWN01 options.report_workers；JOB03/JOB05 |
| IsExecutedPermission | `bool` | DWN01 options.executable |
| IsOverwrite | `bool` | DWN01 options.overwrite |
| DownloadMode | `pcsdownload.DownloadMode` | DWN01 options.mode |
| SaveTo | `string` | DWN01 destination |
| Parallel | `int` | DWN01 options.parallel |
| Load | `int` | DWN01 options.file_concurrency |
| MaxRetry | `int` | DWN01 options.max_retries |
| NoCheck | `bool` | DWN01 options.verify_checksum 的反向映射 |
| ModifyMTime | `bool` | DWN01 options.preserve_mtime |
| FullPath | `bool` | DWN01 options.path_mode |
| LinkPrefer | `int` | DWN01 options.link_index |

### LocateDownloadOption

[internal/pcscommand/download.go:39](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/download.go#L39)

| 字段 | 类型 | HTTP 或内部表达 |
| --- | --- | --- |
| FromPan | `bool` | DWN02 source=pan |

### ExportOptions

[internal/pcscommand/export.go:27](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/export.go#L27)

| 字段 | 类型 | HTTP 或内部表达 |
| --- | --- | --- |
| RootPath | `string` | EXP01 options.root_path；空串保留原路径，允许相对的虚拟导出根 |
| SavePath | `string` | EXP01 options.destination 或 artifact |
| MaxRetry | `int` | EXP01 options.max_retries |
| Recursive | `bool` | EXP01 options.recursive |
| LinkFormat | `bool` | EXP01 options.format=legacy_link |
| StdOut | `bool` | ART02 获取结果内容 |

### LsOptions

[internal/pcscommand/ls_search.go:16](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/ls_search.go#L16)

| 字段 | 类型 | HTTP 或内部表达 |
| --- | --- | --- |
| Total | `bool` | FIL01 fields=all 和当前页 summary；CLI 可聚合分页保持原输出 |

### SearchOptions

[internal/pcscommand/ls_search.go:21](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/ls_search.go#L21)

| 字段 | 类型 | HTTP 或内部表达 |
| --- | --- | --- |
| Total | `bool` | FIL04 fields=all 和当前页 summary |
| Recurse | `bool` | FIL04 recursive |

### Runner

[internal/pcscommand/runner.go:17](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/runner.go#L17)

| 字段 | 类型 | HTTP 或内部表达 |
| --- | --- | --- |
| Output | `io.Writer` | CLI 输出器或 HTTP 结果写入器；内部依赖 |
| IsBackground | `bool` | HTTP 使用 JOB 体系；不公开裸 goroutine 开关 |

### TreeOptions

[internal/pcscommand/tree.go:16](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/tree.go#L16)

| 字段 | 类型 | HTTP 或内部表达 |
| --- | --- | --- |
| Depth | `int` | FIL05 options.depth |
| ShowFsid | `bool` | FIL05 options.include_ids，结果总有可用 ID |

### UploadOptions

[internal/pcscommand/upload.go:28](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/upload.go#L28)

| 字段 | 类型 | HTTP 或内部表达 |
| --- | --- | --- |
| Parallel | `int` | UPL01 options.parallel_per_file |
| MaxRetry | `int` | UPL01 options.max_retries |
| Load | `int` | UPL01 options.file_concurrency |
| NoRapidUpload | `bool` | UPL01 options.skip_rapid_check |
| NoSplitFile | `bool` | UPL01 options.no_split_file；当前未生效，请求 true 返回 422，待独立策略实现 |
| Policy | `string` | UPL01 options.policy |
| NoFilenameCheck | `bool` | UPL01 options.check_filename 的反向映射，覆盖当前全局 IgnoreIllegal 的来源 |

### ListTask

[internal/pcscommand/util.go:21](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/util.go#L21)

| 字段 | 类型 | HTTP 或内部表达 |
| --- | --- | --- |
| ID | `int` | JOB02 job_id、JOB03 item_id 替代内部临时整数 ID |
| MaxRetry | `int` | EXP01 options.max_retries（ListTask 仅用于当前导出队列） |

## 6 baidupcs 全部导出函数和方法

同名 `String`、`JSON` 方法按 receiver 分别列出。`Prepare*` 是请求准备和原始响应流接口，其业务由同一领域的结构化 API 覆盖；HTTP 不返回 Go 对象、Cookie Jar 或内部百度签名令牌。

### baidupcs/baidupcs.go

| 导出函数或方法 | 对应能力 | 处理方式 | 要求 | 源码 |
| --- | --- | --- | --- | --- |
| *BaiduPCS.UpdatePCSCookies | TRF01、TRF02、DWN01 | 内部适配 | 同账号 Cookie 同步，禁止跨账号 Jar 复用 | [baidupcs/baidupcs.go:170](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L170) |
| NewPCS | ACC01、ACC06；所有账号域 | 内部适配 | ClientFactory 按账号、凭据和配置版本创建实例 | [baidupcs/baidupcs.go:215](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L215) |
| NewPCSWithClient | ACC01、ACC06；所有账号域 | 内部适配 | ClientFactory 按账号、凭据和配置版本创建实例 | [baidupcs/baidupcs.go:233](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L233) |
| NewPCSWithCookieStr | ACC01、ACC06；所有账号域 | 内部适配 | ClientFactory 按账号、凭据和配置版本创建实例 | [baidupcs/baidupcs.go:242](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L242) |
| *BaiduPCS.GetClient | 所有账号域 | 内部适配 | 仅适配器读取，普通 HTTP 响应不返回客户端和凭据 | [baidupcs/baidupcs.go:273](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L273) |
| *BaiduPCS.GetBDUSS | 所有账号域 | 内部适配 | 仅适配器读取，普通 HTTP 响应不返回客户端和凭据 | [baidupcs/baidupcs.go:279](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L279) |
| *BaiduPCS.GetBAIDUID | 所有账号域 | 内部适配 | 仅适配器读取，普通 HTTP 响应不返回客户端和凭据 | [baidupcs/baidupcs.go:293](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L293) |
| *BaiduPCS.GetPCSAddr | CFG01、SYS06 | 内部适配 | 客户端地址和节点信息，不公开任意 URL 执行器 | [baidupcs/baidupcs.go:306](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L306) |
| *BaiduPCS.SetAPPID | CFG02；ClientFactory | 内部适配 | 配置生效于对应版本实例，不在请求中改共享对象 | [baidupcs/baidupcs.go:311](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L311) |
| *BaiduPCS.SetUID | ACC01、ACC06；ClientFactory | 内部适配 | 从版本化账号凭据应用，保留大小写不同的现有方法名 | [baidupcs/baidupcs.go:317](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L317) |
| *BaiduPCS.SetaccessToken | ACC01、ACC06；ClientFactory | 内部适配 | 从版本化账号凭据应用，保留大小写不同的现有方法名 | [baidupcs/baidupcs.go:322](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L322) |
| *BaiduPCS.SetStoken | ACC01、ACC06；ClientFactory | 内部适配 | 从版本化账号凭据应用，保留大小写不同的现有方法名 | [baidupcs/baidupcs.go:327](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L327) |
| *BaiduPCS.SetSboxtkn | ACC01、ACC06；ClientFactory | 内部适配 | 从版本化账号凭据应用，保留大小写不同的现有方法名 | [baidupcs/baidupcs.go:343](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L343) |
| *BaiduPCS.SetPCSUserAgent | CFG02；ClientFactory | 内部适配 | 配置生效于对应版本实例，不在请求中改共享对象 | [baidupcs/baidupcs.go:359](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L359) |
| *BaiduPCS.SetPCSAddr | CFG02；ClientFactory | 内部适配 | 配置生效于对应版本实例，不在请求中改共享对象 | [baidupcs/baidupcs.go:364](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L364) |
| *BaiduPCS.CopyPCS | UPL01；ClientFactory | 内部适配 | 当前浅拷贝只能限于同账号用途，不能作为独立账号隔离 | [baidupcs/baidupcs.go:370](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L370) |
| *BaiduPCS.SetPanUserAgent | CFG02；ClientFactory | 内部适配 | 配置生效于对应版本实例，不在请求中改共享对象 | [baidupcs/baidupcs.go:388](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L388) |
| *BaiduPCS.SetHTTPS | CFG02；ClientFactory | 内部适配 | 配置生效于对应版本实例，不在请求中改共享对象 | [baidupcs/baidupcs.go:394](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L394) |
| *BaiduPCS.SetStaticPCSAddr | CFG02；ClientFactory | 内部适配 | 配置生效于对应版本实例，不在请求中改共享对象 | [baidupcs/baidupcs.go:398](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L398) |
| *BaiduPCS.URL | CFG01、SYS06 | 内部适配 | 客户端地址和节点信息，不公开任意 URL 执行器 | [baidupcs/baidupcs.go:403](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L403) |
| *BaiduPCS.UK | ACC07 | HTTP 和内部缓存 | 百度身份读取，未验证和固定 UID 后备账号分开 | [baidupcs/baidupcs.go:474](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L474) |
| *BaiduPCS.BDSToken | TRF01、TRF02、DWN02 | 内部适配 | 签名令牌只在百度适配器中使用，不提供令牌读取 API | [baidupcs/baidupcs.go:501](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/baidupcs.go#L501) |

### baidupcs/cache.go

| 导出函数或方法 | 对应能力 | 处理方式 | 要求 | 源码 |
| --- | --- | --- | --- | --- |
| *BaiduPCS.CacheFilesDirectoriesList | FIL01、FIL05 | 内部适配 | 账号和路径隔离缓存，写操作后失效 | [baidupcs/cache.go:22](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/cache.go#L22) |
| *BaiduPCS.CacheUK | ACC07 | HTTP 和内部缓存 | 百度身份读取，未验证和固定 UID 后备账号分开 | [baidupcs/cache.go:37](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/cache.go#L37) |

### baidupcs/cloud_dl.go

| 导出函数或方法 | 对应能力 | 处理方式 | 要求 | 源码 |
| --- | --- | --- | --- | --- |
| *BaiduPCS.CloudDlAddTask | OFF01 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/cloud_dl.go:119](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/cloud_dl.go#L119) |
| *BaiduPCS.CloudDlQueryTask | OFF03、OFF07 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/cloud_dl.go:198](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/cloud_dl.go#L198) |
| *BaiduPCS.CloudDlListTask | OFF02 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/cloud_dl.go:203](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/cloud_dl.go#L203) |
| *BaiduPCS.CloudDlCancelTask | OFF04 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/cloud_dl.go:266](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/cloud_dl.go#L266) |
| *BaiduPCS.CloudDlDeleteTask | OFF05 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/cloud_dl.go:271](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/cloud_dl.go#L271) |
| *BaiduPCS.CloudDlClearTask | OFF06 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/cloud_dl.go:276](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/cloud_dl.go#L276) |
| *CloudDlTaskInfo.ParseText | OFF02、FIL02；CLI | 内部适配 | 显示和序列化辅助，HTTP 使用结构化 DTO | [baidupcs/cloud_dl.go:298](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/cloud_dl.go#L298) |
| CloudDlTaskList.String | OFF02、FIL02；CLI | 内部适配 | 显示和序列化辅助，HTTP 使用结构化 DTO | [baidupcs/cloud_dl.go:321](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/cloud_dl.go#L321) |

### baidupcs/cp_mv_rename.go

| 导出函数或方法 | 对应能力 | 处理方式 | 要求 | 源码 |
| --- | --- | --- | --- | --- |
| *BaiduPCS.Rename | FIL11 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/cp_mv_rename.go:9](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/cp_mv_rename.go#L9) |
| *BaiduPCS.Copy | FIL09 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/cp_mv_rename.go:17](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/cp_mv_rename.go#L17) |
| *BaiduPCS.Move | FIL10 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/cp_mv_rename.go:22](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/cp_mv_rename.go#L22) |

### baidupcs/download.go

| 导出函数或方法 | 对应能力 | 处理方式 | 要求 | 源码 |
| --- | --- | --- | --- | --- |
| *URLInfo.URLStrings | DWN02、DWN01 | 内部适配 | 完整候选链接和 link_index，不固定只给第一个链接 | [baidupcs/download.go:57](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/download.go#L57) |
| *URLInfo.SingleURL | DWN02、DWN01 | 内部适配 | 完整候选链接和 link_index，不固定只给第一个链接 | [baidupcs/download.go:73](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/download.go#L73) |
| *URLInfo.LastURL | DWN02、DWN01 | 内部适配 | 完整候选链接和 link_index，不固定只给第一个链接 | [baidupcs/download.go:92](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/download.go#L92) |
| *BaiduPCS.DownloadFile | DWN01、DWN03 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/download.go:106](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/download.go#L106) |
| *BaiduPCS.DownloadStreamFile | DWN01、DWN03 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/download.go:117](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/download.go#L117) |
| *BaiduPCS.LocateDownload | DWN02、DWN01 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/download.go:128](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/download.go#L128) |
| *BaiduPCS.LocatePanAPIDownload | DWN02、DWN01 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/download.go:151](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/download.go#L151) |

### baidupcs/extends.go

| 导出函数或方法 | 对应能力 | 处理方式 | 要求 | 源码 |
| --- | --- | --- | --- | --- |
| *BaiduPCS.ExportByFileInfo | EXP01 | HTTP 业务适配 | 条件能力；代码存在不等于当前上游对所有文件可用 | [baidupcs/extends.go:66](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/extends.go#L66) |
| *BaiduPCS.GetRapidUploadInfoByFileInfo | INT01、EXP01 | HTTP 业务适配 | 条件能力；代码存在不等于当前上游对所有文件可用 | [baidupcs/extends.go:86](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/extends.go#L86) |
| *BaiduPCS.GetDirectDownloadLink | DWN02 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/extends.go:121](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/extends.go#L121) |
| *BaiduPCS.GetRapidUploadInfoByLink | INT05、INT01 | HTTP 业务适配 | 条件能力；代码存在不等于当前上游对所有文件可用 | [baidupcs/extends.go:130](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/extends.go#L130) |
| *BaiduPCS.FixMD5ByFileInfo | INT02 | HTTP 业务适配 | 条件能力；代码存在不等于当前上游对所有文件可用 | [baidupcs/extends.go:271](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/extends.go#L271) |
| *BaiduPCS.FixMD5 | INT02 | HTTP 业务适配 | 条件能力；代码存在不等于当前上游对所有文件可用 | [baidupcs/extends.go:322](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/extends.go#L322) |
| *BaiduPCS.MatchPathByShellPattern | FIL06；任务 selector | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/extends.go:358](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/extends.go#L358) |

### baidupcs/file_directory.go

| 导出函数或方法 | 对应能力 | 处理方式 | 要求 | 源码 |
| --- | --- | --- | --- | --- |
| *BaiduPCS.FilesDirectoriesMeta | FIL02 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/file_directory.go:110](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/file_directory.go#L110) |
| *BaiduPCS.FilesDirectoriesBatchMeta | FIL03 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/file_directory.go:132](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/file_directory.go#L132) |
| *BaiduPCS.FilesDirectoriesList | FIL01 | HTTP 业务适配 | 保留当前聚合分页；HTTP 单页另加最小 page 扩展并复用解析 | [baidupcs/file_directory.go:208](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/file_directory.go#L208) |
| *BaiduPCS.FilesDirectoriesListPage | FIL01、FIL05；CLI 聚合读取 | P3 HTTP 业务适配 | 单页复用当前解析、MD5 规范化、排序；聚合读取的重复页/中途错误不得伪报完整 | [baidupcs/file_directory.go:248](../baidupcs/file_directory.go:248) |
| *BaiduPCS.Search | FIL04 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/file_directory.go:257](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/file_directory.go#L257) |
| *BaiduPCS.FilesDirectoriesRecurseList | FIL05、UPL01、DWN01、EXP01 | HTTP 业务适配 | 回调逐项错误必须保存；大遍历进入 job，不能吞错后伪报完整 | [baidupcs/file_directory.go:314](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/file_directory.go#L314) |
| *FileDirectory.String | FIL02；CLI | HTTP 等价表达 | 结构化元数据代替终端表格 | [baidupcs/file_directory.go:342](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/file_directory.go#L342) |
| FileDirectoryList.TotalSize | FIL01、FIL04、FIL05、各批量任务 | HTTP 等价表达 | 转换为 summary 和路径列表；统计范围注明本页或全量 | [baidupcs/file_directory.go:392](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/file_directory.go#L392) |
| FileDirectoryList.Count | FIL01、FIL04、FIL05、各批量任务 | HTTP 等价表达 | 转换为 summary 和路径列表；统计范围注明本页或全量 | [baidupcs/file_directory.go:410](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/file_directory.go#L410) |
| FileDirectoryList.AllFilePaths | FIL01、FIL04、FIL05、各批量任务 | HTTP 等价表达 | 转换为 summary 和路径列表；统计范围注明本页或全量 | [baidupcs/file_directory.go:433](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/file_directory.go#L433) |

### baidupcs/jsontable.go

| 导出函数或方法 | 对应能力 | 处理方式 | 要求 | 源码 |
| --- | --- | --- | --- | --- |
| *PathsListJSON.JSON | FIL08、FIL09、FIL10、UPL01 | 内部适配 | 上游 JSON 编码和关联目录缓存失效 | [baidupcs/jsontable.go:60](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/jsontable.go#L60) |
| *CpMvJSON.JSON | FIL08、FIL09、FIL10、UPL01 | 内部适配 | 上游 JSON 编码和关联目录缓存失效 | [baidupcs/jsontable.go:74](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/jsontable.go#L74) |
| *CpMvListJSON.JSON | FIL08、FIL09、FIL10、UPL01 | 内部适配 | 上游 JSON 编码和关联目录缓存失效 | [baidupcs/jsontable.go:80](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/jsontable.go#L80) |
| *CpMvListJSON.String | OFF02、FIL02；CLI | 内部适配 | 显示和序列化辅助，HTTP 使用结构化 DTO | [baidupcs/jsontable.go:85](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/jsontable.go#L85) |
| *CpMvJSONList.AllRelatedDir | FIL08、FIL09、FIL10、UPL01 | 内部适配 | 上游 JSON 编码和关联目录缓存失效 | [baidupcs/jsontable.go:103](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/jsontable.go#L103) |

### baidupcs/prepare.go

| 导出函数或方法 | 对应能力 | 处理方式 | 要求 | 源码 |
| --- | --- | --- | --- | --- |
| *BaiduPCS.PrepareUK | ACC07 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:100](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L100) |
| *BaiduPCS.PreparePCSServers | SYS06 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:118](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L118) |
| *BaiduPCS.PrepareQuotaInfo | ACC08 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:131](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L131) |
| *BaiduPCS.PrepareFilesDirectoriesBatchMeta | FIL03 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:141](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L141) |
| *BaiduPCS.PrepareFilesDirectoriesList | FIL01 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:166](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L166) |
| *BaiduPCS.PrepareFilesDirectoriesDiff | FIL12 | 内部适配 | 补完整结构化增量模型和上游验证，不用函数存在代替可用 | [baidupcs/prepare.go:200](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L200) |
| *BaiduPCS.PrepareBDStoken | TRF02、DWN02 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:220](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L220) |
| *BaiduPCS.PrepareSearch | FIL04 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:232](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L232) |
| *BaiduPCS.PrepareRemove | FIL08 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:252](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L252) |
| *BaiduPCS.PrepareMkdir | FIL07 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:272](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L272) |
| *BaiduPCS.PrepareRename | FIL11 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:316](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L316) |
| *BaiduPCS.PrepareCopy | FIL09 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:324](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L324) |
| *BaiduPCS.PrepareMove | FIL10 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:329](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L329) |
| *BaiduPCS.PrepareRapidUpload | INT06 | 内部适配 | 旧式接口，INT06 默认上游不可用；不等同上传流程中的 V2 快速检查 | [baidupcs/prepare.go:421](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L421) |
| *BaiduPCS.PrepareRapidUploadV2 | UPL01 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:432](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L432) |
| *BaiduPCS.PrepareFakeRapidUploadV2 | UPL01 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:442](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L442) |
| *BaiduPCS.PrepareLocateDownload | DWN02、DWN01 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:453](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L453) |
| *BaiduPCS.PrepareLocatePanAPIDownload | DWN02、DWN01 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:498](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L498) |
| *BaiduPCS.PrepareUpload | UPL01 | 内部适配 | 保留为内部兼容上传原语；NoSplitFile 独立策略当前未生效 | [baidupcs/prepare.go:526](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L526) |
| *BaiduPCS.PrepareUploadTmpFile | UPL01 | 内部适配 | 保留为内部兼容上传原语；NoSplitFile 独立策略当前未生效 | [baidupcs/prepare.go:554](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L554) |
| *BaiduPCS.PrepareUploadCreateSuperFile | UPL01 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:580](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L580) |
| *BaiduPCS.PrepareUploadPrecreate | UPL01 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:605](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L605) |
| *BaiduPCS.PrepareUploadSuperfile2 | UPL01 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:631](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L631) |
| *BaiduPCS.PrepareCloudDlAddTask | OFF01 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:661](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L661) |
| *BaiduPCS.PrepareCloudDlQueryTask | OFF03 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:678](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L678) |
| *BaiduPCS.PrepareCloudDlListTask | OFF02 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:692](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L692) |
| *BaiduPCS.PrepareCloudDlCancelTask | OFF04 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:720](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L720) |
| *BaiduPCS.PrepareCloudDlDeleteTask | OFF05 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:725](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L725) |
| *BaiduPCS.PrepareCloudDlClearTask | OFF06 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:730](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L730) |
| *BaiduPCS.PrepareSharePSet | SHR01 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:740](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L740) |
| *BaiduPCS.PrepareShareCancel | SHR04 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:763](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L763) |
| *BaiduPCS.PrepareShareList | SHR02 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:783](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L783) |
| *BaiduPCS.PrepareShareSURLInfo | SHR03 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:804](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L804) |
| *BaiduPCS.PrepareRecycleList | REC01 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:824](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L824) |
| *BaiduPCS.PrepareRecycleRestore | REC02 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:839](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L839) |
| *BaiduPCS.PrepareRecycleDelete | REC03 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:870](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L870) |
| *BaiduPCS.PrepareRecycleClear | REC04 | 内部适配 | 原始 ReadCloser 的请求、解析、错误和关闭归基础设施；不公开原始流对象 | [baidupcs/prepare.go:885](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go#L885) |

### baidupcs/publicsuffix.go

| 导出函数或方法 | 对应能力 | 处理方式 | 要求 | 源码 |
| --- | --- | --- | --- | --- |
| list.PublicSuffix | 百度客户端 Cookie 管理 | 内部适配 | publicsuffix.List 接口实现，receiver 为未导出类型，不设 HTTP 路由 | [baidupcs/publicsuffix.go:13](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/publicsuffix.go#L13) |
| list.String | 百度客户端 Cookie 管理 | 内部适配 | publicsuffix.List 接口实现，receiver 为未导出类型，不设 HTTP 路由 | [baidupcs/publicsuffix.go:20](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/publicsuffix.go#L20) |

### baidupcs/quota.go

| 导出函数或方法 | 对应能力 | 处理方式 | 要求 | 源码 |
| --- | --- | --- | --- | --- |
| *BaiduPCS.QuotaInfo | ACC08 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/quota.go:14](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/quota.go#L14) |
| *BaiduPCS.SpaceLeftInfo | ACC08、UPL01 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/quota.go:35](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/quota.go#L35) |

### baidupcs/recycle.go

| 导出函数或方法 | 对应能力 | 处理方式 | 要求 | 源码 |
| --- | --- | --- | --- | --- |
| *BaiduPCS.RecycleList | REC01 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/recycle.go:47](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/recycle.go#L47) |
| *BaiduPCS.RecycleRestore | REC02 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/recycle.go:69](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/recycle.go#L69) |
| *BaiduPCS.RecycleDelete | REC03 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/recycle.go:87](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/recycle.go#L87) |
| *BaiduPCS.RecycleClear | REC04 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/recycle.go:100](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/recycle.go#L100) |

### baidupcs/rm_mkdir.go

| 导出函数或方法 | 对应能力 | 处理方式 | 要求 | 源码 |
| --- | --- | --- | --- | --- |
| *BaiduPCS.Remove | FIL08 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/rm_mkdir.go:9](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/rm_mkdir.go#L9) |
| *BaiduPCS.Mkdir | FIL07 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/rm_mkdir.go:28](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/rm_mkdir.go#L28) |

### baidupcs/share.go

| 导出函数或方法 | 对应能力 | 处理方式 | 要求 | 源码 |
| --- | --- | --- | --- | --- |
| *BaiduPCS.ShareSet | SHR01 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/share.go:72](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/share.go#L72) |
| *BaiduPCS.ShareCancel | SHR04 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/share.go:106](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/share.go#L106) |
| *BaiduPCS.ShareList | SHR02 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/share.go:119](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/share.go#L119) |
| *BaiduPCS.ShareSURLInfo | SHR03 | HTTP 业务适配 | 转为应用服务结果，映射上游错误并绑定账号 | [baidupcs/share.go:156](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/share.go#L156) |

### baidupcs/transfer.go

| 导出函数或方法 | 对应能力 | 处理方式 | 要求 | 源码 |
| --- | --- | --- | --- | --- |
| *BaiduPCS.GenerateShareQueryURL | TRF01、TRF02 | 内部适配 | 完整分享转存编排，临时令牌和请求 URL 不成为公开原始接口 | [baidupcs/transfer.go:28](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/transfer.go#L28) |
| *BaiduPCS.ExtractShareInfo | TRF01、TRF02 | 内部适配 | 完整分享转存编排，临时令牌和请求 URL 不成为公开原始接口 | [baidupcs/transfer.go:43](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/transfer.go#L43) |
| *BaiduPCS.PostShareQuery | TRF01、TRF02 | 内部适配 | 完整分享转存编排，临时令牌和请求 URL 不成为公开原始接口 | [baidupcs/transfer.go:94](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/transfer.go#L94) |
| *BaiduPCS.AccessSharePage | TRF01、TRF02 | 内部适配 | 完整分享转存编排，临时令牌和请求 URL 不成为公开原始接口 | [baidupcs/transfer.go:120](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/transfer.go#L120) |
| *BaiduPCS.GenerateRequestQuery | TRF01、TRF02 | 内部适配 | 完整分享转存编排，临时令牌和请求 URL 不成为公开原始接口 | [baidupcs/transfer.go:164](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/transfer.go#L164) |
| *BaiduPCS.SuperTransfer | TRF02 能力记录 | 实验内部实现 | 当前调用未启用；不默认发布或承诺可用 | [baidupcs/transfer.go:238](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/transfer.go#L238) |

### baidupcs/upload.go

| 导出函数或方法 | 对应能力 | 处理方式 | 要求 | 源码 |
| --- | --- | --- | --- | --- |
| *BaiduPCS.RapidUpload | UPL01 | 内部适配 | 当前预创建、快速检查、分片和合并流程；不把分片握手拆成任意公网调用 | [baidupcs/upload.go:105](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/upload.go#L105) |
| *BaiduPCS.FakeRapidUpload | UPL01 | 内部适配 | 当前预创建、快速检查、分片和合并流程；不把分片握手拆成任意公网调用 | [baidupcs/upload.go:117](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/upload.go#L117) |
| *BaiduPCS.RapidUploadNoCheckDir | INT06 | 历史条件能力 | 旧式摘要上传；当前独立秒传不可承诺可用 | [baidupcs/upload.go:159](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/upload.go#L159) |
| *BaiduPCS.UploadTmpFile | UPL01 | 内部适配 | 当前预创建、快速检查、分片和合并流程；不把分片握手拆成任意公网调用 | [baidupcs/upload.go:176](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/upload.go#L176) |
| *BaiduPCS.UploadCreateSuperFile | UPL01 | 内部适配 | 当前预创建、快速检查、分片和合并流程；不把分片握手拆成任意公网调用 | [baidupcs/upload.go:205](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/upload.go#L205) |
| *BaiduPCS.GetRandomPCSHost | UPL01、SYS06 | 内部适配 | 上传节点选择，诊断可返回脱敏节点 | [baidupcs/upload.go:226](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/upload.go#L226) |

### baidupcs/util.go

| 导出函数或方法 | 对应能力 | 处理方式 | 要求 | 源码 |
| --- | --- | --- | --- | --- |
| *BaiduPCS.Isdir | FIL02、UPL01、DWN01 | 内部适配 | 目录判断和同名策略保护；正常 skip 不归为失败 | [baidupcs/util.go:37](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/util.go#L37) |
| *BaiduPCS.CheckIsdir | FIL02、UPL01、DWN01 | 内部适配 | 目录判断和同名策略保护；正常 skip 不归为失败 | [baidupcs/util.go:50](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/util.go#L50) |
| CreatePasswd | SHR01 | 内部适配 | 分享密码生成；服务 API token 使用独立安全随机源 | [baidupcs/util.go:125](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/util.go#L125) |
| GetHTTPScheme | FIL02、DWN02、UPL01、CFG02 | 内部适配 | 协议、摘要和节点辅助函数，无独立业务路由 | [baidupcs/util.go:135](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/util.go#L135) |
| DecryptMD5 | FIL02、DWN02、UPL01、CFG02 | 内部适配 | 协议、摘要和节点辅助函数，无独立业务路由 | [baidupcs/util.go:142](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/util.go#L142) |
| RandomElement | FIL02、DWN02、UPL01、CFG02 | 内部适配 | 协议、摘要和节点辅助函数，无独立业务路由 | [baidupcs/util.go:168](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/util.go#L168) |

## 7 pcscommand 全部导出函数

| 函数 | 对应能力 | 重构要求 | 源码 |
| --- | --- | --- | --- |
| RunChangeDirectory | CTX03 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/cd.go:10](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/cd.go#L10) |
| RunCloudDlAddTask | OFF01 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/cloud_dl.go:9](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/cloud_dl.go#L9) |
| RunCloudDlQueryTask | OFF03 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/cloud_dl.go:33](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/cloud_dl.go#L33) |
| RunCloudDlListTask | OFF02 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/cloud_dl.go:44](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/cloud_dl.go#L44) |
| RunCloudDlCancelTask | OFF04 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/cloud_dl.go:55](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/cloud_dl.go#L55) |
| RunCloudDlDeleteTask | OFF05 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/cloud_dl.go:68](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/cloud_dl.go#L68) |
| RunCloudDlClearTask | OFF06 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/cloud_dl.go:81](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/cloud_dl.go#L81) |
| RunCopy | FIL09 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/cp_mv.go:12](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/cp_mv.go#L12) |
| RunMove | FIL10、FIL11 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/cp_mv.go:17](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/cp_mv.go#L17) |
| RunDownload | DWN01 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/download.go:52](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/download.go#L52) |
| GetExportFilename | EXP01、ART01 | 内部适配，不把全局当前用户作为 HTTP 上下文 | [internal/pcscommand/export.go:74](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/export.go#L74) |
| RunExport | EXP01 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/export.go:79](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/export.go#L79) |
| RunFixMD5 | INT02 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/fixmd5.go:9](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/fixmd5.go#L9) |
| RunLocateDownload | DWN02 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/locatedonwload.go:13](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/locatedonwload.go#L13) |
| RunLogin | LOG01 至 LOG06 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/login.go:33](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/login.go#L33) |
| RunLs | FIL01 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/ls_search.go:33](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/ls_search.go#L33) |
| RunSearch | FIL04 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/ls_search.go:57](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/ls_search.go#L57) |
| RunGetMeta | FIL02、FIL03 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/meta.go:8](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/meta.go#L8) |
| GetActiveUser | CLI 适配；HTTP 使用显式 account_id | 内部适配，不把全局当前用户作为 HTTP 上下文 | [internal/pcscommand/pcscommand.go:15](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/pcscommand.go#L15) |
| GetBaiduPCS | CLI 适配；HTTP 使用 ClientFactory | 内部适配，不把全局当前用户作为 HTTP 上下文 | [internal/pcscommand/pcscommand.go:20](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/pcscommand.go#L20) |
| RunGetQuota | ACC08 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/quota.go:9](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/quota.go#L9) |
| RunRecycleList | REC01 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/recycle.go:15](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/recycle.go#L15) |
| RunRecycleRestore | REC02 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/recycle.go:42](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/recycle.go#L42) |
| RunRecycleDelete | REC03 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/recycle.go:63](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/recycle.go#L63) |
| RunRecycleClear | REC04 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/recycle.go:78](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/recycle.go#L78) |
| RunRemove | FIL08 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/rm_mkdir.go:11](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/rm_mkdir.go#L11) |
| RunMkdir | FIL07 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/rm_mkdir.go:40](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/rm_mkdir.go#L40) |
| RunShareSet | SHR01 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/share.go:16](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/share.go#L16) |
| RunShareCancel | SHR04 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/share.go:36](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/share.go#L36) |
| RunShareList | SHR02 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/share.go:52](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/share.go#L52) |
| RunShareTransfer | TRF01、TRF02 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/transfer.go:15](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/transfer.go#L15) |
| RunTree | FIL05 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/tree.go:73](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/tree.go#L73) |
| RunUpload | UPL01 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/upload.go:54](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/upload.go#L54) |
| RunTestShellPattern | FIL06 | 抽出业务结果，CLI 保留打印；登录输入和上传信号归入口 | [internal/pcscommand/util.go:32](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcscommand/util.go#L32) |

## 8 pcsconfig 全部导出函数和方法

| 方法 | 归属 | 要求 | 源码 |
| --- | --- | --- | --- |
| *Baidu.BaiduPCS | ClientFactory | 按显式账号和不可变快照构造；保留 CLI 兼容适配 | [internal/pcsconfig/baidu.go:50](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/baidu.go#L50) |
| *Baidu.GetSavePath | DWN01 | 默认保存路径等价映射到允许根目录，保留账号目录及相对层级 | [internal/pcsconfig/baidu.go:70](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/baidu.go#L70) |
| *Baidu.PathJoin | FIL 系列、CTX 系列 | 使用显式 base_path 或拥有者 context，不读取进程 cwd | [internal/pcsconfig/baidu.go:80](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/baidu.go#L80) |
| NewUserInfoByBDUSS | ACC01、ACC06、ACC07、LOG05 | 凭据和账号仓库适配；后备身份不得按固定 UID 覆盖其他账号 | [internal/pcsconfig/baidu.go:91](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/baidu.go#L91) |
| NewUserInfoByInput | ACC01、ACC06、ACC07、LOG05 | 凭据和账号仓库适配；后备身份不得按固定 UID 覆盖其他账号 | [internal/pcsconfig/baidu.go:111](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/baidu.go#L111) |
| *BaiduUserList.String | CLI；ACC02、CFG01 | 显示函数保留 CLI，HTTP 输出具体 DTO | [internal/pcsconfig/baidu.go:126](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/baidu.go#L126) |
| *PCSConfig.ActiveUser | CTX02、CTX03、CFG09；CLI | HTTP 使用调用方 context，修改 CLI 默认值与运行任务分开 | [internal/pcsconfig/export.go:15](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/export.go#L15) |
| *PCSConfig.ActiveUserBaiduPCS | ClientFactory | 按显式账号和不可变快照构造；保留 CLI 兼容适配 | [internal/pcsconfig/export.go:23](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/export.go#L23) |
| *PCSConfig.HTTPClient | ClientFactory | 按显式账号和不可变快照构造；保留 CLI 兼容适配 | [internal/pcsconfig/export.go:38](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/export.go#L38) |
| *PCSConfig.PCSHTTPClient | ClientFactory | 按显式账号和不可变快照构造；保留 CLI 兼容适配 | [internal/pcsconfig/export.go:43](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/export.go#L43) |
| *PCSConfig.PanHTTPClient | ClientFactory | 按显式账号和不可变快照构造；保留 CLI 兼容适配 | [internal/pcsconfig/export.go:48](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/export.go#L48) |
| *PCSConfig.NumLogins | ACC02、ACC03、ACC05 | AccountRepository 按本地 account_id 管理，限制调用方可见账号 | [internal/pcsconfig/export.go:53](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/export.go#L53) |
| *PCSConfig.AverageParallel | DWN01 | 下载总并发预算分配；内部策略 | [internal/pcsconfig/export.go:58](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/export.go#L58) |
| *PCSConfig.PrintTable | CLI；ACC02、CFG01 | 显示函数保留 CLI，HTTP 输出具体 DTO | [internal/pcsconfig/export.go:63](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/export.go#L63) |
| *PCSConfig.SwitchUser | CTX02、CTX03、CFG09；CLI | HTTP 使用调用方 context，修改 CLI 默认值与运行任务分开 | [internal/pcsconfig/maniper.go:97](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L97) |
| *PCSConfig.DeleteUser | ACC02、ACC03、ACC05 | AccountRepository 按本地 account_id 管理，限制调用方可见账号 | [internal/pcsconfig/maniper.go:102](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L102) |
| *PCSConfig.GetBaiduUser | ACC02、ACC03、ACC05 | AccountRepository 按本地 account_id 管理，限制调用方可见账号 | [internal/pcsconfig/maniper.go:107](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L107) |
| *PCSConfig.CheckBaiduUserExist | ACC02、ACC03、ACC05 | AccountRepository 按本地 account_id 管理，限制调用方可见账号 | [internal/pcsconfig/maniper.go:112](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L112) |
| *PCSConfig.SetupUserByBDUSS | ACC01、ACC06、ACC07、LOG05 | 凭据和账号仓库适配；后备身份不得按固定 UID 覆盖其他账号 | [internal/pcsconfig/maniper.go:118](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L118) |
| *PCSConfig.SetAppID | CFG01、CFG02、CFG04；配置适配 | 应用服务先校验再提交，不在 handler 中原地修改全局 Config | [internal/pcsconfig/maniper.go:148](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L148) |
| *PCSConfig.SetCacheSizeByStr | CFG01、CFG02、CFG04；配置适配 | 应用服务先校验再提交，不在 handler 中原地修改全局 Config | [internal/pcsconfig/maniper.go:156](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L156) |
| *PCSConfig.SetMaxDownloadRateByStr | CFG01、CFG02、CFG04；配置适配 | 应用服务先校验再提交，不在 handler 中原地修改全局 Config | [internal/pcsconfig/maniper.go:166](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L166) |
| *PCSConfig.SetMaxUploadRateByStr | CFG01、CFG02、CFG04；配置适配 | 应用服务先校验再提交，不在 handler 中原地修改全局 Config | [internal/pcsconfig/maniper.go:176](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L176) |
| *PCSConfig.SetUserAgent | CFG01、CFG02、CFG04；配置适配 | 应用服务先校验再提交，不在 handler 中原地修改全局 Config | [internal/pcsconfig/maniper.go:186](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L186) |
| *PCSConfig.SetPCSUA | CFG01、CFG02、CFG04；配置适配 | 应用服务先校验再提交，不在 handler 中原地修改全局 Config | [internal/pcsconfig/maniper.go:192](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L192) |
| *PCSConfig.SetPanUA | CFG01、CFG02、CFG04；配置适配 | 应用服务先校验再提交，不在 handler 中原地修改全局 Config | [internal/pcsconfig/maniper.go:200](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L200) |
| *PCSConfig.SETPCSAddr | CFG01、CFG02、CFG04；配置适配 | 应用服务先校验再提交，不在 handler 中原地修改全局 Config | [internal/pcsconfig/maniper.go:208](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L208) |
| *PCSConfig.SetStaticPCSAddr | CFG01、CFG02、CFG04；配置适配 | 应用服务先校验再提交，不在 handler 中原地修改全局 Config | [internal/pcsconfig/maniper.go:220](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L220) |
| *PCSConfig.SetEnableHTTPS | CFG01、CFG02、CFG04；配置适配 | 应用服务先校验再提交，不在 handler 中原地修改全局 Config | [internal/pcsconfig/maniper.go:228](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L228) |
| *PCSConfig.SetNoCheck | CFG01、CFG02、CFG04；配置适配 | 应用服务先校验再提交，不在 handler 中原地修改全局 Config | [internal/pcsconfig/maniper.go:235](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L235) |
| *PCSConfig.SetUploadPolicy | CFG01、CFG02、CFG04；配置适配 | 应用服务先校验再提交，不在 handler 中原地修改全局 Config | [internal/pcsconfig/maniper.go:240](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L240) |
| *PCSConfig.SetProxy | CFG01、CFG02、CFG04；配置适配 | 应用服务先校验再提交，不在 handler 中原地修改全局 Config | [internal/pcsconfig/maniper.go:245](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L245) |
| *PCSConfig.SetProxyHostnames | CFG01、CFG02、CFG04；配置适配 | 应用服务先校验再提交，不在 handler 中原地修改全局 Config | [internal/pcsconfig/maniper.go:251](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L251) |
| *PCSConfig.SetLocalAddrs | CFG01、CFG02、CFG04；配置适配 | 应用服务先校验再提交，不在 handler 中原地修改全局 Config | [internal/pcsconfig/maniper.go:257](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L257) |
| *PCSConfig.SetIgnoreIllegal | CFG01、CFG02、CFG04；配置适配 | 应用服务先校验再提交，不在 handler 中原地修改全局 Config | [internal/pcsconfig/maniper.go:263](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L263) |
| *PCSConfig.SetForceLogin | CFG01、CFG02、CFG04；配置适配 | 应用服务先校验再提交，不在 handler 中原地修改全局 Config | [internal/pcsconfig/maniper.go:268](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/maniper.go#L268) |
| NewConfig | Bootstrap、CFG05、CFG06、CFG07 | 生命周期、固定配置来源、锁和原子持久化；不提供任意文件读写 | [internal/pcsconfig/pcsconfig.go:72](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/pcsconfig.go#L72) |
| *PCSConfig.Init | Bootstrap、CFG05、CFG06、CFG07 | 生命周期、固定配置来源、锁和原子持久化；不提供任意文件读写 | [internal/pcsconfig/pcsconfig.go:80](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/pcsconfig.go#L80) |
| *PCSConfig.Reload | Bootstrap、CFG05、CFG06、CFG07 | 生命周期、固定配置来源、锁和原子持久化；不提供任意文件读写 | [internal/pcsconfig/pcsconfig.go:85](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/pcsconfig.go#L85) |
| *PCSConfig.Close | Bootstrap、CFG05、CFG06、CFG07 | 生命周期、固定配置来源、锁和原子持久化；不提供任意文件读写 | [internal/pcsconfig/pcsconfig.go:90](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/pcsconfig.go#L90) |
| *PCSConfig.Save | Bootstrap、CFG05、CFG06、CFG07 | 生命周期、固定配置来源、锁和原子持久化；不提供任意文件读写 | [internal/pcsconfig/pcsconfig.go:100](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/pcsconfig.go#L100) |
| *PCSConfig.InitDefaultConfig | Bootstrap、CFG05、CFG06、CFG07 | 生命周期、固定配置来源、锁和原子持久化；不提供任意文件读写 | [internal/pcsconfig/pcsconfig.go:231](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/pcsconfig.go#L231) |
| GetConfigDir | Bootstrap、CFG05、CFG06、CFG07 | 生命周期、固定配置来源、锁和原子持久化；不提供任意文件读写 | [internal/pcsconfig/pcsconfig.go:270](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/pcsconfig.go#L270) |
| AverageParallel | DWN01 | 下载总并发预算分配；内部策略 | [internal/pcsconfig/util.go:9](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsconfig/util.go#L9) |

## 9 传输和调度内部接口

### pcsupload

| 方法 | 所属 HTTP 能力 | 内部处理 | 源码 |
| --- | --- | --- | --- |
| GracefulStopActiveUploaders | UPL01、JOB 系列 | 只供服务关闭；单 job 取消禁止使用此全局停止入口 | [internal/pcsfunctions/pcsupload/active.go:18](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/active.go#L18) |
| RegisterActiveUploader | UPL01、JOB 系列 | 服务模式改为按 job 持有执行句柄，避免全局停止污染 | [internal/pcsfunctions/pcsupload/active.go:28](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/active.go#L28) |
| UnregisterActiveUploader | UPL01、JOB 系列 | 服务模式改为按 job 持有执行句柄，避免全局停止污染 | [internal/pcsfunctions/pcsupload/active.go:36](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/active.go#L36) |
| EmptyReaderLen64.Read | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [internal/pcsfunctions/pcsupload/upload.go:44](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/upload.go#L44) |
| EmptyReaderLen64.Len | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [internal/pcsfunctions/pcsupload/upload.go:48](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/upload.go#L48) |
| NewPCSUpload | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [internal/pcsfunctions/pcsupload/upload.go:52](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/upload.go#L52) |
| *PCSUpload.Precreate | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [internal/pcsfunctions/pcsupload/upload.go:66](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/upload.go#L66) |
| *PCSUpload.TmpFile | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [internal/pcsfunctions/pcsupload/upload.go:73](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/upload.go#L73) |
| *PCSUpload.CreateSuperFile | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [internal/pcsfunctions/pcsupload/upload.go:126](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/upload.go#L126) |
| NewUploadingDatabase | UPL01、JOB 系列 | 服务增加账号、目标和指纹命名空间及统一管理；不并发各开旧全局文件 | [internal/pcsfunctions/pcsupload/upload_database.go:35](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/upload_database.go#L35) |
| *UploadingDatabase.Save | UPL01、JOB 系列 | 服务增加账号、目标和指纹命名空间及统一管理；不并发各开旧全局文件 | [internal/pcsfunctions/pcsupload/upload_database.go:65](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/upload_database.go#L65) |
| *UploadingDatabase.UpdateUploading | UPL01、JOB 系列 | 服务增加账号、目标和指纹命名空间及统一管理；不并发各开旧全局文件 | [internal/pcsfunctions/pcsupload/upload_database.go:98](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/upload_database.go#L98) |
| *UploadingDatabase.UpdateFullBlock | UPL01、JOB 系列 | 服务增加账号、目标和指纹命名空间及统一管理；不并发各开旧全局文件 | [internal/pcsfunctions/pcsupload/upload_database.go:122](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/upload_database.go#L122) |
| *UploadingDatabase.Delete | UPL01、JOB 系列 | 服务增加账号、目标和指纹命名空间及统一管理；不并发各开旧全局文件 | [internal/pcsfunctions/pcsupload/upload_database.go:150](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/upload_database.go#L150) |
| *UploadingDatabase.Search | UPL01、JOB 系列 | 服务增加账号、目标和指纹命名空间及统一管理；不并发各开旧全局文件 | [internal/pcsfunctions/pcsupload/upload_database.go:170](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/upload_database.go#L170) |
| *UploadingDatabase.Close | UPL01、JOB 系列 | 服务增加账号、目标和指纹命名空间及统一管理；不并发各开旧全局文件 | [internal/pcsfunctions/pcsupload/upload_database.go:235](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/upload_database.go#L235) |
| *UploadTaskUnit.SetTaskInfo | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [internal/pcsfunctions/pcsupload/upload_task_unit.go:69](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/upload_task_unit.go#L69) |
| *UploadTaskUnit.OnRetry | UPL01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [internal/pcsfunctions/pcsupload/upload_task_unit.go:434](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/upload_task_unit.go#L434) |
| *UploadTaskUnit.OnSuccess | UPL01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [internal/pcsfunctions/pcsupload/upload_task_unit.go:444](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/upload_task_unit.go#L444) |
| *UploadTaskUnit.OnFailed | UPL01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [internal/pcsfunctions/pcsupload/upload_task_unit.go:447](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/upload_task_unit.go#L447) |
| *UploadTaskUnit.OnComplete | UPL01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [internal/pcsfunctions/pcsupload/upload_task_unit.go:457](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/upload_task_unit.go#L457) |
| *UploadTaskUnit.RetryWait | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [internal/pcsfunctions/pcsupload/upload_task_unit.go:460](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/upload_task_unit.go#L460) |
| *UploadTaskUnit.Run | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [internal/pcsfunctions/pcsupload/upload_task_unit.go:474](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsupload/upload_task_unit.go#L474) |

### pcsdownload

| 方法 | 所属 HTTP 能力 | 内部处理 | 源码 |
| --- | --- | --- | --- |
| GetLocateDownloadLinks | DWN01、DWN02 | 当前主项目链接逻辑和备选顺序 | [internal/pcsfunctions/pcsdownload/download_link.go:9](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsdownload/download_link.go#L9) |
| *DownloadTaskUnit.SetTaskInfo | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [internal/pcsfunctions/pcsdownload/download_task_unit.go:90](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsdownload/download_task_unit.go#L90) |
| *DownloadTaskUnit.OnRetry | DWN01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [internal/pcsfunctions/pcsdownload/download_task_unit.go:423](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsdownload/download_task_unit.go#L423) |
| *DownloadTaskUnit.OnSuccess | DWN01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [internal/pcsfunctions/pcsdownload/download_task_unit.go:433](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsdownload/download_task_unit.go#L433) |
| *DownloadTaskUnit.OnFailed | DWN01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [internal/pcsfunctions/pcsdownload/download_task_unit.go:436](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsdownload/download_task_unit.go#L436) |
| *DownloadTaskUnit.OnComplete | DWN01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [internal/pcsfunctions/pcsdownload/download_task_unit.go:446](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsdownload/download_task_unit.go#L446) |
| *DownloadTaskUnit.RetryWait | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [internal/pcsfunctions/pcsdownload/download_task_unit.go:449](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsdownload/download_task_unit.go#L449) |
| *DownloadTaskUnit.Run | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [internal/pcsfunctions/pcsdownload/download_task_unit.go:453](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsdownload/download_task_unit.go#L453) |
| IsSkipMd5Checksum | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [internal/pcsfunctions/pcsdownload/pcsdownload.go:11](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsdownload/pcsdownload.go#L11) |
| BaiduPCSURLCheckFunc | DWN01、JOB 系列 | 下载节点和 Cookie 处理；重定向和域名授权需要服务边界校验 | [internal/pcsfunctions/pcsdownload/pcsdownload.go:22](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsdownload/pcsdownload.go#L22) |
| CheckFileValid | DWN01、INT04 | 校验长度和摘要，明确忽略校验的有效选项 | [internal/pcsfunctions/pcsdownload/utils.go:17](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsdownload/utils.go#L17) |
| FileExist | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [internal/pcsfunctions/pcsdownload/utils.go:47](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsdownload/utils.go#L47) |
| FixHTTPLinkURL | DWN01、JOB 系列 | 下载节点和 Cookie 处理；重定向和域名授权需要服务边界校验 | [internal/pcsfunctions/pcsdownload/utils.go:61](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsdownload/utils.go#L61) |
| CloneJarWithDomain | DWN01、JOB 系列 | 下载节点和 Cookie 处理；重定向和域名授权需要服务边界校验 | [internal/pcsfunctions/pcsdownload/utils.go:69](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/internal/pcsfunctions/pcsdownload/utils.go#L69) |

### taskframework

| 方法 | 所属 HTTP 能力 | 内部处理 | 源码 |
| --- | --- | --- | --- |
| NewTaskExecutor | JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [pcsutil/taskframework/executor.go:25](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/pcsutil/taskframework/executor.go#L25) |
| *TaskExecutor.SetParallel | JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [pcsutil/taskframework/executor.go:45](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/pcsutil/taskframework/executor.go#L45) |
| *TaskExecutor.Append | JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [pcsutil/taskframework/executor.go:50](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/pcsutil/taskframework/executor.go#L50) |
| *TaskExecutor.AppendNoRetry | JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [pcsutil/taskframework/executor.go:65](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/pcsutil/taskframework/executor.go#L65) |
| *TaskExecutor.Count | JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [pcsutil/taskframework/executor.go:70](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/pcsutil/taskframework/executor.go#L70) |
| *TaskExecutor.Execute | JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [pcsutil/taskframework/executor.go:78](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/pcsutil/taskframework/executor.go#L78) |
| *TaskExecutor.FailedDeque | JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [pcsutil/taskframework/executor.go:164](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/pcsutil/taskframework/executor.go#L164) |
| *TaskExecutor.Stop | JOB 系列 | 接 JOB06 至 JOB08；支持情况按具体执行器及运行阶段声明 | [pcsutil/taskframework/executor.go:169](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/pcsutil/taskframework/executor.go#L169) |
| *TaskExecutor.Stopped | JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [pcsutil/taskframework/executor.go:174](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/pcsutil/taskframework/executor.go#L174) |
| *TaskExecutor.Pause | JOB 系列 | 当前方法为空；不得当作已实现暂停恢复 | [pcsutil/taskframework/executor.go:179](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/pcsutil/taskframework/executor.go#L179) |
| *TaskExecutor.Resume | JOB 系列 | 当前方法为空；不得当作已实现暂停恢复 | [pcsutil/taskframework/executor.go:184](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/pcsutil/taskframework/executor.go#L184) |
| *TaskInfo.IsExceedRetry | JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [pcsutil/taskframework/taskinfo.go:17](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/pcsutil/taskframework/taskinfo.go#L17) |
| *TaskInfo.Id | JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [pcsutil/taskframework/taskinfo.go:21](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/pcsutil/taskframework/taskinfo.go#L21) |
| *TaskInfo.MaxRetry | JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [pcsutil/taskframework/taskinfo.go:25](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/pcsutil/taskframework/taskinfo.go#L25) |
| *TaskInfo.SetMaxRetry | JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [pcsutil/taskframework/taskinfo.go:29](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/pcsutil/taskframework/taskinfo.go#L29) |
| *TaskInfo.Retry | JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [pcsutil/taskframework/taskinfo.go:33](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/pcsutil/taskframework/taskinfo.go#L33) |

### uploader

| 方法 | 所属 HTTP 能力 | 内部处理 | 源码 |
| --- | --- | --- | --- |
| SplitBlock | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/block.go:38](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/block.go#L38) |
| NewBufioSplitUnit | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/block.go:53](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/block.go#L53) |
| *bufioFileBlock.Read | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/block.go:66](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/block.go#L66) |
| *fileBlock.Read | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/block.go:71](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/block.go#L71) |
| *fileBlock.Seek | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/block.go:97](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/block.go#L97) |
| *fileBlock.Len | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/block.go:117](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/block.go#L117) |
| *fileBlock.Left | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/block.go:121](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/block.go#L121) |
| *fileBlock.Range | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/block.go:125](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/block.go#L125) |
| *fileBlock.Readed | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/block.go:129](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/block.go#L129) |
| *MultiError.Error | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/error.go:12](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/error.go#L12) |
| DoUpload | UPL01、JOB 系列 | 示例/便捷执行器；服务复用底层执行能力，不引入任意 URL 下载入口 | [requester/uploader/example.go:10](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/example.go#L10) |
| NewMultiUploader | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/multiuploader.go:61](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/multiuploader.go#L61) |
| *MultiUploader.SetInstanceState | UPL01、JOB 系列 | 由 checkpoint store 管理；恢复先验证来源、目标和文件指纹 | [requester/uploader/multiuploader.go:76](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/multiuploader.go#L76) |
| *MultiUploader.Execute | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/multiuploader.go:117](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/multiuploader.go#L117) |
| *MultiUploader.InstanceState | UPL01、JOB 系列 | 由 checkpoint store 管理；恢复先验证来源、目标和文件指纹 | [requester/uploader/multiuploader.go:163](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/multiuploader.go#L163) |
| *MultiUploader.Cancel | UPL01、JOB 系列 | 接 JOB06 至 JOB08；支持情况按具体执行器及运行阶段声明 | [requester/uploader/multiuploader.go:179](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/multiuploader.go#L179) |
| *MultiUploader.GracefulStop | UPL01、JOB 系列 | 接 JOB06 至 JOB08；支持情况按具体执行器及运行阶段声明 | [requester/uploader/multiuploader.go:186](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/multiuploader.go#L186) |
| *MultiUploader.OnExecute | UPL01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [requester/uploader/multiuploader.go:203](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/multiuploader.go#L203) |
| *MultiUploader.OnSuccess | UPL01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [requester/uploader/multiuploader.go:208](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/multiuploader.go#L208) |
| *MultiUploader.OnFinish | UPL01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [requester/uploader/multiuploader.go:213](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/multiuploader.go#L213) |
| *MultiUploader.OnCancel | UPL01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [requester/uploader/multiuploader.go:218](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/multiuploader.go#L218) |
| *MultiUploader.OnError | UPL01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [requester/uploader/multiuploader.go:223](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/multiuploader.go#L223) |
| *MultiUploader.OnUploadStatusEvent | UPL01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [requester/uploader/multiuploader.go:228](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/multiuploader.go#L228) |
| *workerList.CheckSumList | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/multiworker.go:25](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/multiworker.go#L25) |
| *workerList.Readed | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/multiworker.go:33](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/multiworker.go#L33) |
| NewReaded64 | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/readed.go:22](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/readed.go#L22) |
| *readed64.Read | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/readed.go:29](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/readed.go#L29) |
| *readed64.Readed | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/readed.go:35](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/readed.go#L35) |
| *UploadStatus.TotalSize | UPL01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [requester/uploader/status.go:28](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/status.go#L28) |
| *UploadStatus.Uploaded | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/status.go:33](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/status.go#L33) |
| *UploadStatus.SpeedsPerSecond | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/status.go:38](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/status.go#L38) |
| *UploadStatus.TimeElapsed | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/status.go:43](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/status.go#L43) |
| *Uploader.GetStatusChan | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/status.go:48](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/status.go#L48) |
| NewUploader | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/uploader.go:46](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/uploader.go#L46) |
| *Uploader.SetClient | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/uploader.go:67](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/uploader.go#L67) |
| *Uploader.SetContentType | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/uploader.go:72](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/uploader.go#L72) |
| *Uploader.SetCheckFunc | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/uploader.go:77](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/uploader.go#L77) |
| *Uploader.Execute | UPL01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/uploader/uploader.go:82](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/uploader.go#L82) |
| *Uploader.OnExecute | UPL01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [requester/uploader/uploader.go:116](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/uploader.go#L116) |
| *Uploader.OnFinish | UPL01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [requester/uploader/uploader.go:121](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/uploader/uploader.go#L121) |

### downloader

| 方法 | 所属 HTTP 能力 | 内部处理 | 源码 |
| --- | --- | --- | --- |
| *Downloader.ExecuteContext | DWN01、JOB06、JOB08 | P2内部单连接执行：实例客户端、context取消、严格Range和调用方确认检查点；旧Execute不变 | [requester/downloader/context.go:16](../requester/downloader/context.go:16) |
| NewConfig | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/config.go:32](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/config.go#L32) |
| *Config.Fix | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/config.go:41](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/config.go#L41) |
| *Config.Copy | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/config.go:49](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/config.go#L49) |
| NewDownloadFirstInfoByResp | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/download_firstinfo.go:19](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/download_firstinfo.go#L19) |
| *DownloadFirstInfo.Compare | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/download_firstinfo.go:33](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/download_firstinfo.go#L33) |
| *DownloadFirstInfo.ToMap | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/download_firstinfo.go:50](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/download_firstinfo.go#L50) |
| *DownloadFirstInfo.ToMapByReflect | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/download_firstinfo.go:61](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/download_firstinfo.go#L61) |
| NewDownloader | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/downloader.go:62](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L62) |
| *Downloader.SetFirstInfo | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/downloader.go:74](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L74) |
| *Downloader.SetClient | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/downloader.go:79](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L79) |
| *Downloader.SetDURLCheckFunc | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/downloader.go:84](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L84) |
| *Downloader.SetFileContentLength | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/downloader.go:88](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L88) |
| *Downloader.SetLoadBalancerCompareFunc | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/downloader.go:98](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L98) |
| *Downloader.SetStatusCodeBodyCheckFunc | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/downloader.go:103](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L103) |
| *Downloader.SelectParallel | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/downloader.go:127](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L127) |
| *Downloader.SelectBlockSizeAndInitRangeGen | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/downloader.go:147](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L147) |
| *Downloader.SelectCacheSize | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/downloader.go:183](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L183) |
| DefaultDURLCheckFunc | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/downloader.go:194](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L194) |
| *Downloader.Execute | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/downloader.go:286](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L286) |
| *Downloader.Pause | DWN01、JOB 系列 | 接 JOB06 至 JOB08；支持情况按具体执行器及运行阶段声明 | [requester/downloader/downloader.go:498](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L498) |
| *Downloader.Resume | DWN01、JOB 系列 | 接 JOB06 至 JOB08；支持情况按具体执行器及运行阶段声明 | [requester/downloader/downloader.go:507](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L507) |
| *Downloader.Cancel | DWN01、JOB 系列 | 接 JOB06 至 JOB08；支持情况按具体执行器及运行阶段声明 | [requester/downloader/downloader.go:516](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L516) |
| *Downloader.OnExecute | DWN01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [requester/downloader/downloader.go:525](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L525) |
| *Downloader.OnSuccess | DWN01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [requester/downloader/downloader.go:530](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L530) |
| *Downloader.OnFinish | DWN01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [requester/downloader/downloader.go:535](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L535) |
| *Downloader.OnPause | DWN01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [requester/downloader/downloader.go:540](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L540) |
| *Downloader.OnResume | DWN01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [requester/downloader/downloader.go:545](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L545) |
| *Downloader.OnCancel | DWN01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [requester/downloader/downloader.go:550](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L550) |
| *Downloader.OnDownloadStatusEvent | DWN01、JOB 系列 | 接观察者和持久化 job 事件；替代通过终端输出来判断结果 | [requester/downloader/downloader.go:555](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/downloader.go#L555) |
| DoDownload | DWN01、JOB 系列 | 示例/便捷执行器；服务复用底层执行能力，不引入任意 URL 下载入口 | [requester/downloader/example.go:12](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/example.go#L12) |
| NewInstanceState | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/instance_state.go:35](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/instance_state.go#L35) |
| *InstanceState.Get | DWN01、JOB 系列 | 由 checkpoint store 管理；恢复先验证来源、目标和文件指纹 | [requester/downloader/instance_state.go:69](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/instance_state.go#L69) |
| *InstanceState.Put | DWN01、JOB 系列 | 由 checkpoint store 管理；恢复先验证来源、目标和文件指纹 | [requester/downloader/instance_state.go:101](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/instance_state.go#L101) |
| *InstanceState.Close | DWN01、JOB 系列 | 由 checkpoint store 管理；恢复先验证来源、目标和文件指纹 | [requester/downloader/instance_state.go:139](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/instance_state.go#L139) |
| NewLoadBalancerResponseList | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/loadbalance.go:25](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/loadbalance.go#L25) |
| *LoadBalancerResponseList.SequentialGet | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/loadbalance.go:32](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/loadbalance.go#L32) |
| *LoadBalancerResponseList.RandomGet | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/loadbalance.go:47](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/loadbalance.go#L47) |
| *Downloader.AddLoadBalanceServer | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/loadbalance.go:52](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/loadbalance.go#L52) |
| DefaultLoadBalancerCompareFunc | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/loadbalance.go:57](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/loadbalance.go#L57) |
| NewMonitor | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/monitor.go:37](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L37) |
| *Monitor.InitMonitorCapacity | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/monitor.go:55](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L55) |
| *Monitor.Append | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/monitor.go:60](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L60) |
| *Monitor.SetWorkers | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/monitor.go:68](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L68) |
| *Monitor.SetStatus | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/monitor.go:73](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L73) |
| *Monitor.SetInstanceState | DWN01、JOB 系列 | 由 checkpoint store 管理；恢复先验证来源、目标和文件指纹 | [requester/downloader/monitor.go:78](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L78) |
| *Monitor.Status | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/monitor.go:83](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L83) |
| *Monitor.Err | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/monitor.go:88](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L88) |
| *Monitor.CompletedChan | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/monitor.go:93](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L93) |
| *Monitor.GetAvailableWorker | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/monitor.go:98](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L98) |
| *Monitor.GetAllWorkersRange | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/monitor.go:112](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L112) |
| *Monitor.NumLeftWorkers | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/monitor.go:121](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L121) |
| *Monitor.SetReloadWorker | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/monitor.go:131](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L131) |
| *Monitor.IsLeftWorkersAllFailed | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/monitor.go:136](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L136) |
| *Monitor.ResetFailedAndNetErrorWorkers | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/monitor.go:188](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L188) |
| *Monitor.RangeWorker | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/monitor.go:212](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L212) |
| *Monitor.Pause | DWN01、JOB 系列 | 接 JOB06 至 JOB08；支持情况按具体执行器及运行阶段声明 | [requester/downloader/monitor.go:221](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L221) |
| *Monitor.Resume | DWN01、JOB 系列 | 接 JOB06 至 JOB08；支持情况按具体执行器及运行阶段声明 | [requester/downloader/monitor.go:228](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L228) |
| *Monitor.TryAddNewWork | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/monitor.go:235](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L235) |
| *Monitor.DynamicSplitWorker | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/monitor.go:269](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L269) |
| *Monitor.ResetWorker | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/monitor.go:310](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L310) |
| *Monitor.Execute | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/monitor.go:345](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/monitor.go#L345) |
| NewResetController | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/resetcontroler.go:18](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/resetcontroler.go#L18) |
| *ResetController.AddResetNum | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/resetcontroler.go:35](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/resetcontroler.go#L35) |
| *ResetController.CanReset | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/resetcontroler.go:43](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/resetcontroler.go#L43) |
| WorkerList.Len | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/sort.go:11](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/sort.go#L11) |
| WorkerList.Swap | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/sort.go:16](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/sort.go#L16) |
| ByLeftDesc.Less | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/sort.go:21](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/sort.go#L21) |
| GetStatusText | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/status.go:54](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/status.go#L54) |
| NewWorkerStatus | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/status.go:86](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/status.go#L86) |
| *WorkerStatus.SetStatusCode | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/status.go:93](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/status.go#L93) |
| *WorkerStatus.StatusCode | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/status.go:98](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/status.go#L98) |
| *WorkerStatus.StatusText | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/status.go:103](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/status.go#L103) |
| RandomNumber | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/utils.go:27](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/utils.go#L27) |
| GetFileName | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/utils.go:35](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/utils.go#L35) |
| ParseContentRange | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/utils.go:67](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/utils.go#L67) |
| WorkerList.Duplicate | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/worker.go:47](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L47) |
| NewWorker | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/worker.go:54](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L54) |
| *Worker.ID | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/worker.go:63](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L63) |
| *Worker.SetTotalSize | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/worker.go:88](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L88) |
| *Worker.SetClient | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/worker.go:93](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L93) |
| *Worker.SetAcceptRange | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/worker.go:98](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L98) |
| *Worker.SetRange | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/worker.go:103](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L103) |
| *Worker.SetReferer | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/worker.go:113](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L113) |
| *Worker.SetWriteMutex | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/worker.go:118](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L118) |
| *Worker.SetDownloadStatus | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/worker.go:123](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L123) |
| *Worker.GetStatus | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/worker.go:128](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L128) |
| *Worker.GetRange | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/worker.go:134](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L134) |
| *Worker.GetSpeedsPerSecond | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/worker.go:139](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L139) |
| *Worker.Pause | DWN01、JOB 系列 | 接 JOB06 至 JOB08；支持情况按具体执行器及运行阶段声明 | [requester/downloader/worker.go:144](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L144) |
| *Worker.Resume | DWN01、JOB 系列 | 接 JOB06 至 JOB08；支持情况按具体执行器及运行阶段声明 | [requester/downloader/worker.go:159](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L159) |
| *Worker.Cancel | DWN01、JOB 系列 | 接 JOB06 至 JOB08；支持情况按具体执行器及运行阶段声明 | [requester/downloader/worker.go:167](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L167) |
| *Worker.Reset | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/worker.go:179](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L179) |
| *Worker.Canceled | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/worker.go:193](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L193) |
| *Worker.Completed | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/worker.go:198](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L198) |
| *Worker.Failed | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/worker.go:208](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L208) |
| *Worker.ClearStatus | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/worker.go:218](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L218) |
| *Worker.Err | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/worker.go:223](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L223) |
| *Worker.Execute | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/worker.go:228](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/worker.go#L228) |
| NewDownloaderWriterByFilename | DWN01、JOB 系列 | 内部执行和状态适配，不为 Go 方法建立独立公网路由 | [requester/downloader/writer.go:21](https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/requester/downloader/writer.go#L21) |

### 底层配置和回调

| 对象 | 完整处理 |
| --- | --- |
| uploader.MultiUpload | Precreate、TmpFile、CreateSuperFile 全部由 UploadAdapter 调用，账号和 uploadid 绑定；不对客户端开放任意握手 |
| taskframework.TaskUnit | SetTaskInfo、Run、RetryWait、OnRetry、OnSuccess、OnFailed、OnComplete 全部由 job 执行和观察者适配；Result 中的 Succeed、NeedRetry、NeedNextdindex、Err、ResultCode、ResultMessage、Extra 转为明确状态与错误 |
| downloader.Config | MaxParallel、CacheSize、MaxRate、IsTest、TryHTTP 映射有效配置；Mode、BlockSize 是受控策略；InstanceStateStorageFormat、InstanceStatePath 是服务断点内部实现 |
| uploader.MultiUploaderConfig | Parallel、MaxRate、Policy 映射有效任务选项；BlockSize 依主项目分片阈值和文件大小策略，不接受客户端无界数值 |
| DownloadFunc、UploadFunc、HandleFileDirectoryFunc | 作为适配器回调，不序列化为 HTTP 参数；对应内容、传输和扫描业务 |
| FileDirectory、Shared、ShareSURLInfo、CloudDlTaskInfo、RecycleFDInfo | 映射各领域 DTO；Go unsafe 解析对象、原始 remote error 和内部共享指针不直接输出 |

## 10 工具和公共基础设施归属

| 代码范围 | 归属 | 覆盖要求 |
| --- | --- | --- |
| pcsutil/checksum | INT03、INT04、UPL01、DWN01 | 文件长度、完整 MD5、slice MD5、CRC32、分块 MD5；file/meta/reader 生命周期由适配器管理 |
| pcsutil/crypto.go | LOC02、LOC03 | 九种已有 AES CTR/CFB/OFB 兼容方法和 gzip；密钥秘密化、源文件删除有明确选项 |
| pcsutil/file.go | LOC01、UPL01、DWN01 | 遍历、软链接、路径合法性、平台路径和磁盘访问；不公开任意文件系统方法 |
| pcsutil/getip 和 pcsutil/addr.go | SYS05 | 授权网络诊断，使用配置网络客户端和明确检查超时 |
| internal/pcsfunctions/common.go 与 statistic.go | UPL01、DWN01、JOB 系列 | 重试等待、原子进度、计时和速率转换成观察者事件 |
| baidupcs/pcserror | 所有领域的错误适配 | 保留 operation、ErrType、远端错误码，转换稳定 Problem Details |
| baidupcs/netdisksign 与 baidupcs/internal/panhome | 登录、下载和分享适配器 | 签名、BDSToken 和缓存生命周期，账号隔离，不开放原始签名端点 |
| baidupcs/expires、cachemap、pcsutil/cachepool 等 | 基础设施内部 | TTL、缓存、内存池、等待组和转换；按业务访问边界调用 |
| requester/http_client、dial、fetch、rio、multipartreader、transfer | ClientFactory 和传输适配器 | 网络、代理、Cookie、读写、限速和 Range；不公开通用 HTTP 代理或 Go 对象接口 |
| pcsliner、pcstable、pcsverbose | CLI 或服务运维 | 终端历史、补全和表格保留 CLI；服务使用脱敏日志和指标 |
| internal/pcsupdate、cmd/AndroidNDKBuild、build.sh、资源和平台构建文件 | CLI 更新及构建发布 | 不构成网盘业务 HTTP 入口；构建和发布单独验证 |

## 11 历史能力和额外功能的计数规则

| 项目 | 源码或参考事实 | 处理 |
| --- | --- | --- |
| RunFixMD5 / FixMD5 | 有效函数存在，当前 CLI 没有注册命令 | INT02 条件能力，纳入底层业务覆盖 |
| NoSplitFile | 字段传递存在，实际执行未使用 | true 返回明确不支持；实现独立策略后才计入可用 |
| Rname | 实际调用被注释 | 不把已接受字段当作已支持；true 返回明确不支持 |
| SuperTransfer | 导出实现存在，当前调用注释 | 实验内部实现登记，不默认公开 |
| PCSUpload2 | 文件主体被块注释包围 | 排除有效接口清单 |
| 旧式秒传与秒传链接 | 当前转存流程已拒绝旧格式 | INT06 说明上游不可用；V2 上传快速检查仍属于正常上传流程 |
| 导出大小提示 | 旧 20GB 文案与当前 128 GiB 常量不一致 | 能力限制从实际代码和上游验证分别给出，不传播旧提示为事实 |
| xpan 两个 HTTP 路由 | 参考项目新增，主项目没有同等完整适配 | 可选扩展，不计入主项目必需覆盖率 |
| QR 登录及客户端分片接收 | 服务体验扩展 | 在 P6 实施，单列新增能力而不伪装成当前主项目已有 HTTP 接口 |

## 12 覆盖更新规则

新增或变更业务命令、命令参数、核心导出方法及配置字段时，必须同时检查本表、OpenAPI 和相关验收。升级主项目后先比较 AST 清单，再判断是否影响公开契约；底层重命名不能机械产生新的 HTTP 端点。

实现阶段为每个业务项补充 implementation、offline_test、upstream_test、限制和证据链接。HTTP 无遗漏、逻辑已实现、离线测试通过、百度上游实测通过是四个独立结论。


## 13 P0 补充的数据字段与行为核对

完整字段以 OpenAPI schema 为准；这里补充容易在“导出函数已映射”之后仍然遗漏的结构化数据与调用限制。

| 源码对象/字段 | 目标表达与边界 |
| --- | --- |
| BaiduBase.UID/Name，Baidu.Sex/Age | Account.baidu_uid/username/sex/age；未知可空，身份后备明确 identity_verified=false；年龄不猜单位 |
| Baidu.BDUSS/PTOKEN/STOKEN/BAIDUID/SBOXTKN/COOKIES/AccessToken | ACC01 互斥主凭据导入、ACC06 presence-aware 更新；Account 仅有 credentials_present，不回显秘密 |
| Baidu.Workdir、BaiduActiveUID | 调用方 Context.cwd 与 CFG08/CFG09 的 CLI 默认值分开，不决定其他请求或已入库任务 |
| FileDirectory 的 FsID/AppID/Path/Filename/Ctime/Mtime/MD5/BlockList/Size/Isdir/Ifhassubdir | FileEntry 对应字段和 md5_status；ID/字节用字符串，未知时间可空 |
| FileDirectory.PreBase/Parent/Children | 扫描、树和下载计划内部关系；通过有界 artifact/扫描结果表达，不递归序列化共享指针 |
| ShareRecordInfo 与 ShareSURLInfo | Share 中分别表达列表元数据与短链接/提取码；只有 SURL 信息时省略未知列表字段；period_days 保留任意有效天数 |
| CloudDlTaskInfo 及 FileList/Result | OfflineTask/OfflineBatchItem；Result=不存在或上游缺项均保留逐输入失败；TaskID 使用 offline_task_id |
| CloudDlQueryTask 的 100 项截断 | OFF03 maxItems=100、uniqueItems；101 项 413，不能默默丢掉第 101 项 |
| PrepareCloudDlListTask 固定 start=0/limit=1000 | OFF02 只分页实际收到的快照，不能承诺全部历史任务 |
| RecycleFDInfo.LeftTime | RecycleEntry.upstream_left_time 保留原值，单位未验证不推算 expires_at；恢复保留实际成功 fs_id |
| LocalFileMeta.Length/SliceMD5/BlocksList/MD5/CRC32/ModTime/Path | Checksum 与 JobItem/local_file；slice 为 256 KiB；CRC32 为十进制字符串，local path 不泄露 root 绝对目录 |
| TaskUnitRunResult、DownloadStatus、上传状态与控制句柄 | Job/Progress/JobItem/CheckpointSummary/WorkerStatus；累计数量为字符串、未知总量 null、跳过与失败分开；空 Pause/Resume 不算成功实现 |
| 配置/凭据 revision 与任务有效选项 | 任务提交时固定；普通配置更新不改变已有 job；凭据吊销使旧引用停止执行 |

P0对609条记录完成初始映射；P1增加server及4个启动参数至614条；P2增加ExecuteContext内部原语，当前616条均已映射。105个目标操作中40个implemented、3个partial、62个planned；P3的JSON/SSE/文件/HEAD/204实际响应和逐项验收见[P3记录](http-api-p3-verification.md)，67个原schema边界例及25组完整行为规格继续保留。P2的真实百度结果仅限用户授权账号和专用目录，见独立脱敏报告。Go 1.23.12/1.26.2 下各 14 个既有离线测试包通过，另有 3 项原有失败，详见核对报告。这里不把函数映射率、路由实现率和实际可用性混为一个百分比。


## 14 P1 新分层代码归属

| 目录/入口 | 归属与验证 |
| --- | --- |
| internal/bootstrap | 入口组装、模式判断、服务配置、数据目录锁、HTTP与信号生命周期；根main只做CLI参数和signal.NotifyContext |
| internal/httpapi | 12个系统/配置操作与10个P2 partial handler；Bearer、严格JSON、强ETag、公共账号/Job/Result DTO、Problem及脱敏日志；其余planned操作授权后501 |
| internal/application | 配置校验/继承/版本/幂等编排、服务身份授权与系统能力；不import HTTP或基础设施 |
| internal/domain 与 internal/ports | 拥有其数据的不可变快照、账号凭据、principal、领域错误和存储/认证/环境边界 |
| internal/infrastructure/baidu | ClientFactory每次构建全新PCS/Jar/Transport/缓存及签名状态；测试通过本地TLS上游检查Cookie、UA、配置版本、取消和重定向隔离 |
| requester.NewHTTPClientWithTransport | 加法式实例构造，不读取全局代理、UA、网卡或DNS缓存；旧NewHTTPClient路径保留 |
| internal/infrastructure/persistence | SQLite配置版本/CAS/配置幂等、AES-GCM、私有密钥、单目录独占锁和初始任务表；SQLite仅在5个平台组合导入 |
| internal/infrastructure/identity、localfs、network | SHA-256 token摘要、账号/root权限清单、受限根目录及本机网卡查询 |
| api/openapi/contract.go、openapi.json | 由唯一YAML源生成并嵌入；校验器阻止JSON漂移，启动时校验实现状态与实际handler一致 |

新分层内部函数按上述责任和依赖方向测试管理，不额外制造HTTP路由。P0的402个导出函数与24个配置字段的盘点范围保持；P2账号和受限传输已接入HTTP，完整账号/文件/任务/传输仍按P3及后续阶段核销。


## 15 P2 受限 HTTP 与真实执行链路

| 组件 | 当前范围与证据 |
| --- | --- |
| application/accounts、domain/account | 三种凭据规范化、独立账号ID；Entry的JSON不包含凭据；规范化及错误输入测试 |
| application/transfers、domain/transfer、ports/transfers.go | 2个活动任务、16个等待名额；服务生命周期、幂等提交、独立取消、手动恢复；内部记录不是公网DTO |
| infrastructure/persistence/transfers.go | v1→v2→v3增量迁移；账号、执行记录、配置、检查点和控制重放加密；状态/时间/version比较更新、queued派发与启动中断识别 |
| infrastructure/baidu/transfer.go | 复用当前FakeRapidUpload/UploadTmpFile/UploadCreateSuperFile和MultiUploader；单文件直连下载、摘要、分片/偏移检查点与结果核对 |
| requester/downloader/context.go | 新增独立的单连接context执行路径，不使用旧全局缓存/monitor；拒绝不匹配的恢复Range和响应长度 |
| requester/uploader/multiworker.go | 取消后等待当前provider调用返回再释放源文件；并发终止错误写入加锁 |
| infrastructure/baidu/transfer_test.go | 受控TLS上传回读、A/B/C取消隔离、重开恢复、错误检查点拒绝、远端合并响应丢失核对；race通过 |

上述组件已接入生产server及10个partial操作，12个P1操作保持implemented。明确支持1字节至32MiB普通文件、固定单文件选项、skip、零限速、查询/结果/取消/恢复。持久派发、同目标锁、控制幂等、关闭、文件故障窗口及四个SIGKILL点已经验证；真实百度的账号、上传回读、取消、重启恢复和清理通过，范围见[P2验证记录](http-api-p2-verification.md)及[脱敏结果](http-api-p2-upstream-verification.json)。目录、空文件、完整选项及旧并行下载monitor仍在P4。

P2补充归属：domain/operation表达逻辑变更摘要；httpapi/p2.go、jobs.go负责十个partial的输入/公开映射；application/accounts与transfers处理权限、快照、幂等和派发；persistence/replays.go负责账号/控制同事务重放、过期与活动保留；localfs/open_*固定真实文件边界及不覆盖提交。新增内部函数不凭函数名增加HTTP端点；现有615项盘点边界和历史快照保持。


## 16 P3 完整账号、读取、任务和结果文件

| 组件 | 对应操作与实现/离线证据 |
| --- | --- |
| domain/account、application/accounts、persistence/accounts.go | ACC01—ACC09；presence/null、规范 Jar、强版本、历史凭据和撤销；账号集成与 credential patch 测试 |
| infrastructure/legacyconfig、bootstrap startup | 固定 legacy_accounts_file 只读导入；规范凭据+源条目索引指纹、独立账号ID、持久重放；重复UID、冲突和重开测试 |
| baidupcs.FilesDirectoriesListPage、infrastructure/baidu/files.go、application/files | FIL01—FIL06；共享分页解析、投影事实、绝对/base_path、批量逐项、搜索快照、通配符和扫描；1001/2000/2001、空页、失败和精度测试 |
| application/query、persistence/queries.go | 搜索/任务快照绑定、密文游标、600秒TTL和总配额；大扫描逐项使用不可变条目高水位，避免复制全部结果 |
| application/transfers、persistence/transfers.go/events.go | JOB01—JOB06、JOB09；持久派发继续单实例2执行/16等待；状态/检查点/逐项/控制与事件原子提交、retry新任务，保留P2提交不明与恢复规则 |
| httpapi/events.go | JOB05；snapshot高水位→追踪、Last-Event-ID、410/409/400、终态发送、15秒心跳、64连接和128帧上限；慢消费者与真实本地HTTP流测试 |
| application/artifacts、infrastructure/localfs/artifacts.go、persistence/artifacts.go | ART01—ART04、JOB11；流式JSONL、Range/If-Range/HEAD、TTL/配额、任务和响应引用、owner及原权限、删除摘要保留 |
| application/local、localfs/list.go/space_* | LOC01；仅授权root ID/读写/可用空间状态，绝对路径不进入DTO |
| pcscommand/ls_search.go、meta.go、tree.go | 触及读取路径的小接口适配，继续调用原聚合方法；终端排序、输出、fs_id、递归/深度与失败行为由reads_test.go回归 |
| bootstrap/p3_test.go、persistence/p3_test.go及相关P3测试 | 逐项证据见[P3验收](http-api-p3-verification.md)；原P2受控TLS、取消隔离、SIGKILL窗口和兼容测试继续执行 |

P3只将有完整实现与离线证据的28个操作核销，包含此前4个P2账号partial。UPL01、DWN01、JOB08继续partial，完整传输仍属P4；上游可用性按账号、参数和时间单独认定。P0/P1历史清单不重写，P2真实账号报告不扩写成P3实测。

本轮新扫码会话的单账号小目录实测见[P3脱敏报告](http-api-p3-upstream-verification.json)，33个检查阶段通过；规模分页、故障、TTL、慢消费者与多账号行为仍由各自离线测试核销。
