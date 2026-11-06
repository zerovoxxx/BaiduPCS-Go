# P4 完整传输

契约 `0.4.0-p4`，SQLite v5。P4 的十个核心操作及 DWN05 独立票据扩展均已实现；验收结果见 [P4 验收](http-api-p4-verification.md)。

上传支持 local/staged 单文件、目录和空文件，目录保留源根名；skip 跳过已有文件，overwrite 替换，rsync 按主项目的同大小规则跳过。支持秒传及 norapid、两种握手、分片校验、有限重试和失效 uploadid 重建。NoSplitFile=true 明确422。

下载支持 pcs/locate/stream、paths/fs_ids/patterns、relative/full。多根去重并消除重叠后，以根父目录的最近公共祖先保留层级。没有 flat 模式。支持覆盖、校验、mtime、执行权限、test、worker 结果和限速。test 实际传输校验后删除临时文件；dry_run 固定计划并创建 job，不修改数据。

文件并发最多16，单文件线程最多32，总线程256；服务仍为2个执行任务/16个等待任务。上传文件最大128GiB；目录计划最多10000项，超过413。省略继承配置，显式0重试和false不会被默认值覆盖。每任务限速由该任务的全部工作连接共享，等待可取消。

JOB07 先提交 pausing 意图，执行器排空并保存检查点后才进入 paused；JOB08 给原任务新建网络上下文。提交未知仍 interrupted，并先核验结果。retry 创建新任务、重新准备，不沿用检查点。

UPL02 流式接收单file及可选filename part，顺序均可；独立随机0600文件，单文件256MiB、总量1GiB、128文件。幂等摘要包括文件内容和展示名。任务入库事务内引用暂存文件；paused/interrupted也保持引用；有引用删除409，无引用过期24小时清理。磁盘失败不能发布ready ID。

DWN02 按输入返回直链/错误，直链可能需要百度Cookie，接口不返回Cookie。DWN03/04代理验证HTTPS目标及每次重定向，支持GET/HEAD、单Range、If-Range、206/416。开始流后失败只终止流。DWN05独立扩展的票据默认60秒、最大300秒，仅绑定账号和路径；兑换复核当前调用方权限和账号凭据版本，不能调用其他接口。票据、签名URL和link_ref不写日志。

v4→v5仅新增暂存表及引用，不重建旧数据。升级前停止服务，一致备份master.key、service.db及WAL、artifacts和staging目录；回滚恢复配套的升级前备份。旧检查点、控制回执和普通凭据更新绑定旧版本规则保留。
