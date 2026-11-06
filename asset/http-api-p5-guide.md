# P5 网盘写业务操作指南

契约 `0.5.0-p5`，SQLite schema v6。P5 的23项操作均有业务实现及受控上游验收；实现状态与百度实测范围分别报告，见[逐项验收](http-api-p5-verification.md)。当前全契约74 implemented、0 partial、31 planned。P4十个核心操作与DWN05独立扩展保持交付状态。

## 文件写入与结果

FIL07 创建一个或多个目录；FIL08 支持 paths/patterns/fs_ids 明确选择，`ignore_missing=true`将确知不存在项记skipped；FIL09/10使用逐项from/to；FIL11限定同一父目录，跨目录应调用move。根目录不能修改，不能复制/移动到自身或源的后代。相对路径需要base_path，context仍属P6。

复制、移动、重命名的conflict_policy为fail/skip/overwrite，默认fail。覆盖直接传递PCS的ondup=overwrite，不先删除目标；源和已存在目标在准备阶段保存身份/大小/摘要，执行前再次校验。目录覆盖明确冲突。每次写入前保存提交意图，成功后核验目标可见，再保存逐项结果。单个确知错误不吞掉其他成功项。`baidupcs.cpmvOp`现保留prepare错误，CLI与HTTP共用修复。

所有约定批量写接口返回202 job。通过JOB02/03/04/05读取状态、项目、结果和持久事件；成功/失败/跳过/取消分别计数。MutationResult中的`fs_id`、`offline_task_id`取真实上游返回值。预览最多1000项，完整项使用JOB03游标。JOB09只新建符合scope的失败或取消项，重新准备并绑定当前配置/凭据，不复用检查点。

## 分享和转存

SHR01保持201同步契约，接受0（永久）及正的period_days，不限制为1/7/30，32位正整数范围内的值原样提交，由百度判定实际有效期。显式提取码必须是4个字母或数字，combined_link控制是否将提取码放入返回链接。SHR02沿主项目真实页码语义返回，不虚构total/page_size；SHR03只返回上游能确认的详情字段，其他保持null；SHR04批量取消仍是job。

TRF01解析整段分享文本、URL中的pwd或独立password，优先级为显式password→URL→文本。所有权与账号/凭据版本绑定的inspection有效600秒，上游每100项读取至末页，重复页报错；服务最多10000项，128个有效inspection、每个32MiB、总量128MiB。HTTP cursor在持久快照上分页；directory_fs_id必须来自已检查目录，展开结果以事务合并。凭据换版或过期后重新检查。

TRF02接受inspection_id或原始分享输入。fs_ids必须属于本次完整检查结果，未指定则选择所有根项目；不会只转存首批。collect为多个项目建立本job唯一collection目录，逐项结果保留最终目标。转存后读取目标元数据确认可见，能获得摘要时校验。`download_after=true`同时需要transfer、local.write及目标root授权；普通下载子job仅选择已确认成功的转存项，以parent_job_id/child_job_ids关联，继承提交时解析的下载选项。缺少目标或不支持的下载选项明确报错。

NoSplitFile=true和randomize_name/Rname=true明确422；SuperTransfer没有有效实现，不能当作大分享回退。CLI整段文本解析和分享元数据聚合与HTTP共用核心，CLI同样明确拒绝Rname。

## 离线与回收站

OFF01仅将source URL按主项目CloudDl协议提交给百度，本服务不抓取该URL；磁力等主项目支持的协议原样传递。百度offline_task_id与本地job_id分开，未知ID不伪造。OFF03最多100个规范正整数ID，101个在发出上游请求前413；每个输入均有条目或错误。

OFF02只取得主项目目前真实可获得的start=0/limit=1000窗口，再在服务快照上分页；`meta.upstream_window.complete_history=false`明确不承诺账号全量历史。cursor的has_more仅指此窗口剩余项目。不会为请求第1001项虚构百度翻页。OFF04/05/06分别取消、删除和显式清空，都是job；OFF07读取单个百度ID。

REC01使用真实上游页码和100项页大小。upstream_left_time为原样有符号十进制字符串，deleted_at/expires_at保持null，不猜单位。REC02记录实际恢复返回ID，可能与输入ID不同；REC03永久删除指定ID，REC04显式清空。OFF06/REC04无请求体、需write及账号allowlist，作用于所授权账号全局；不会由普通列表或选择器暗中触发。生产调用方必须明确授予这些已有write接口的使用范围。本轮真实验证不会调用这两个全局清空接口。

## 未知结果与取消

每次网络写入前在既有job检查点中持久化committing意图，收到确知拒绝才允许逐项失败和后续retry。请求丢失、断连或凭据撤销不表示百度回滚；有未知提交的任务保持interrupted，直接cancel/retry不能把它变成无副作用失败。

JOB10只受理已经排空的interrupted任务。dry_run只读核验，不改变任务或检查点；非dry_run把同一job排回已有有界调度器，逐项读取证据，绝不再次提交原写入。文件读取目标/源的身份与内容；分享取消读取已撤销状态；离线新增仅在原1000窗口完整、时间/URL/路径唯一且排除原有及同job已记录ID时关联真实ID。证据不足仍interrupted，而不是自动重试。回收站恢复丢失实际返回ID、全局清空丢失回执等无法可靠区分外部操作的情况保守保留未知状态。

核验使用当前凭据；凭据版本变化时，必须能将当前百度UK与原计划保存UK相互验证，身份未知、UK=0或不同明确拒绝。普通任务仍固定使用旧凭据版本，null撤销语义保留。尚未提交的后续项目在核验后记cancelled/remote_effect=none，可另行JOB09新建任务。取消已排队任务的逐项无副作用结果、事件、控制回执和终态同一事务提交。

Idempotency-Key仍绑定调用方/操作/账号/输入。写任务新增SemanticInput仅用于新P5请求，动态inspection令牌和已删除的模式匹配结果不会破坏原提交重放；历史P2请求空字段序列化不变。SHR01另存加密同步提交意图与回执；未知提交禁止用同Key重新创建分享，保留待人工按分享查询核对。不得把换Key当作安全重试。

## 升级、备份与回滚

v5→v6只新增加密share_inspections和business_mutations；v1—v5均通过增量链升级，不重建任务。v5任务、检查点、控制回执、暂存及引用保持。inspection过期清理，已完成同步回执24小时清理，未知同步提交意图保留，避免过期后重放写入。维护沿现有服务每分钟清理，不增加后台平台。

升级前停止唯一实例，一致备份数据目录的master.key、service.db（存在时含-wal/-shm）、artifacts和staging目录，保留原权限与服务配置。备份不能替换master.key或单独回退DB。先在副本启动新二进制验证；回滚时停止新服务、恢复完整升级前目录及原二进制。旧二进制拒绝schema v6。回滚本地库不代表回滚已经执行的百度事务；未知远端写操作仍需核验，不能恢复后盲目重发。

受控验收和获准真实目录的命令、逐项状态以及证据文件见[P5验收](http-api-p5-verification.md)。服务不借用pcsconfig.Config/pcscommand全局当前账号。P6/P7剩余31个操作继续planned，二维码专项主检出不并入本分支。
