# P6 操作指南

契约 `0.6.0-p6`，SQLite schema v7；P6 的30个操作已实现。此阶段全契约104 implemented、0 partial、1 planned（P7 SYS07）。离线、真实百度和平台构建分别见验收记录。用户原始交接保留在 http-api-p6-p7-handoff.md。

## 摘要、修复、导出和增量

INT01 inspect 接受 paths/base_path/context_id，按输入返回成功或失败。size_bytes 来自内容长度；slice_md5 计算前256KiB；小文件完整读取后 md5_status=verified，大文件的上游 Content-MD5 为reported。缺失MD5/CRC32返回null，不用0填补。INT02修复是逐项持久job，复用当前核心修复协议；缺少必要摘要为CHECKSUM_UNAVAILABLE，未知远端提交保留interrupted，不自动重发。

INT03是root_id/paths的本地摘要job，支持md5、slice_md5、crc32、4MiB block_md5；INT04按items中的path与local_file校验所选算法，缺摘要与内容不一致分别返回逐项CHECKSUM_UNAVAILABLE/CHECKSUM_MISMATCH。普通凭据更新仍绑定提交版本，清除凭据仍撤销旧任务。

INT05只接受DWN02签发的link_ref，无任意URL入口。引用绑定owner、account、凭据版本，10分钟有效、最多256个；读取沿既有HTTPS目标/逐跳校验边界，使用签发时的UA。expected_checksum支持md5/sha256，流式读取完整内容后比较，受请求期限约束，不能把slice摘要当完整摘要。INT06合法请求固定503 UPSTREAM_CAPABILITY_UNAVAILABLE、retryable=false，capability为upstream_unavailable；这是已实现的历史不可用语义。

EXP01支持recursive、root_path、jsonl/legacy_command/legacy_link、max_retries和可选受限destination。默认输出artifact，另存文件名为export_JOB_ID.txt，原子提交、不覆盖。空root_path保留路径，其他root_path只改导出虚拟路径。legacy_link不使用目录改写并在missing_fields报告提示。导出按页遍历，节点100000、栈10000、深度1000、期限3600秒；保留逐项失败和完整性标志，artifact沿用256MiB/总1GiB/128个与TTL、引用保护。导出不包含Cookie、密钥、签名URL或内部状态。

FIL12保留上游cursor、has_more、reset_required，不把失败变为空成功，不以全量列表生成替代游标。未取得真实增量能力时返回明确上游错误。此接口涉及账号整体增量，本轮真实验证未读取它，使用受控协议证据。

## 受限本地加解密

LOC02/03的sources使用root_id+relative_path，destination使用root_id+directory，key只写。支持原项目的9种AES CTR/CFB/OFB、128/192/256位算法及gzip；保留原IV前缀和密钥补零/截断字节格式。默认gzip=true、remove_source=false。解密移除.encrypt后缀；没有该后缀时加.decrypted。输出0600，随机临时文件、不覆盖提交；取消、失败与重启清理未提交临时输出。只有提交和确认成功后，remove_source=true才删除原文件。

原始CTR/CFB/OFB格式没有认证标签；gzip=true时能检查gzip头和CRC，错误密钥/损坏输入不能发布明文。gzip=false保持原格式，其本身不能证明密钥正确或检测所有篡改。临时密钥仅保存在进程内，不进入任务、事件、日志、artifact或持久数据库；私有幂等身份使用服务密钥派生的HMAC，持久记录不含密钥本身；重启后未完成任务明确TEMPORARY_SECRET_LOST，需重新提交密钥，不能用空密钥继续。提交窗口结果未知仍LOCAL_RESULT_UNKNOWN/interrupted，不能自动重试覆盖输出。

## HTTP登录

LOG01创建password或qrcode会话，LOG02查询、LOG03读取服务保存的PNG，LOG04响应challenge_id，LOG05完成账号建立/更新，LOG06撤销。登录不使用CLI全局账号、终端输入或全局信号。会话按owner隔离、TTL300秒不因轮询延长、最多128个；取消/过期销毁交换状态，不能完成。登录状态是进程临时资源，重启需要新建会话。

password支持captcha、短信/邮箱两套原验证流程。收到sms/email挑战后先响应send（或sms/email选择方式）请求发送，再用新的challenge_id响应验证码；未知挑战明确拒绝。qrcode图片和captcha图片都从本服务受权URL读取，响应中不返回上游签名、票据或交换令牌。扫码查询只推进保存的交换状态。complete不能接收临时BDUSS；写入前核验百度有效UK，更新已验证账号时要求身份一致，凭据写入复用账号服务。重复complete返回同一账号，改变目标或标签冲突。真实密码/短信/扫码确认未自动获准，本轮仅受控上游验证这些步骤，未要求重复扫码。

## 上下文和管理

CTX01/02/03/04管理owner的account/cwd；TTL86400秒，更新续期。CTX03必须强If-Match，切换账号时cwd默认/，显式cwd必须绝对；include_listing只附第一页。所有带path scope的文件、传输、分享、转存、离线、校验、导出入口先读取一次context快照；context与base_path互斥，账号不一致409，不能用fs_id选择绕过校验。已接受任务固定账号、路径、选项，context变化不改变任务或相同输入的幂等回执。

CFG08/09映射启动时明确指定的legacy_accounts_file，按导入后的独立account_id对应CLI账号，默认账号及各账号workdir原子写回该固定快照，不共享pcsconfig.Config。必须使用独立受控配置，不指定时CFG09明确不可用，不会寻找或修改日常CLI配置。CLI ETag为完整内容的私有比较标记，重启后更换，不用作任务配置revision。If-Match比较完整快照，外部凭据或账号排序变化使映射失效，需重新装配；重复旧UID不能猜测默认账号。CLI改动不改变HTTP context或运行job。

CFG06只允许source=service/legacy_cli，来源在启动时固定；service文件中的client对象是显式重载候选，dry_run默认为true。请求不能指定文件。身份、roots、监听、TLS、存储变化要求重启；重载也不能更改网络权限边界。先校验和比较版本，再原子提交，失败保留当前配置。

SYS05仅admin可调用；默认不主动外连，私有/回环/未指定网卡地址不公开。active_probe=true使用原CLI固定HTTPS ipify目标，无Cookie、不跟随重定向，5秒期限；不返回代理、接口名、主机路径或秘密。SYS06按账号查询、校验PCS候选域名；无已确认有效时间时expires_at=null，无候选时明确不可用。

## 浏览器续传接收

UPL05创建filename、size_bytes及可选expected_checksum（md5/sha256）；UPL06查询；UPL07在/content发送application/octet-stream和Upload-Offset；UPL08 complete；UPL09取消。单块32MiB，单文件256MiB，共享暂存总量1GiB/128文件。接收与上传百度的检查点独立。未完成不发布staged_file_id；偏移冲突409，超过剩余长度/块限制413。

块写入和fsync完成、数据库提交后才推进offset。失败或断连截回旧偏移；重新打开时截去比DB偏移长的尾部，长度短于已提交偏移明确报错。PATCH/complete按会话串行；complete比较完整长度及约定摘要，完成后重复返回同一个ID。完成到staging与接收状态之间的重启窗口可修复。零字节可直接complete。闲置24小时过期清理，完成文件的活动/paused/interrupted引用仍受P4保护。

## 升级与回滚

v6→v7只增加加密resources表及索引，旧任务、事件、幂等回执、检查点和staging引用保留。停止服务后一起备份master.key、service.db及日志、artifacts、staging（含接收内容）、固定服务/CLI配置及受限本地输出。旧二进制拒绝高版本库；回滚恢复整套升级前备份，不能修改PRAGMA版本号假装降级。P7提供可执行备份/恢复工具和完整核销记录。
