# HTTP API 0.5.0-p5

P4完整传输之后完成P5全部23项文件写入、分享、转存、离线、回收站和JOB10核验。当前74 implemented、0 partial、31 planned；十个核心P4与DWN05独立扩展保持交付。主检出独立QR工作没有合入本分支。

核心修复保留copy/move/rename准备错误并改正app_id十进制编码。文件冲突有明确fail/skip/overwrite与逐项部分成功。分享支持任意有效period、整段文本、全部检查页、fs_id选择、collect和下载子任务；字符串大整数ID不经float。CLI共用文本解析并修复首批截断，Rname/NoSplitFile/SuperTransfer继续明确拒绝未实现能力。

离线只向百度提交URL，区分百度task_id和job_id；查询超过100项拒绝，列表显式限定当前1000项窗口。回收站保存实际恢复ID和原始LeftTime，不猜时间单位。批量写入保持job、逐项、持久事件、幂等和所有权；JobItem扩展可选写结果字段，原P3字段保持。

未知提交保持interrupted，JOB10只读核验，不能证明时不重发原写。普通凭据更新仍绑定旧版本，核验换版校验百度UK，null撤销规则不变。SHR01同步创建有加密持久提交意图及回执，未知意图不因TTL清理后重新提交。取消不代表百度回滚。

schema v6为v5加法迁移，旧v1—v5链路保留。停机一致备份master.key、DB及其日志、artifacts和staging；旧二进制拒绝v6，回滚需恢复升级前配套备份，远端事务不会随本地回滚撤销。

完整使用、逐项受控/真实证据和构建结果见[P5指南](http-api-p5-guide.md)、[验收](http-api-p5-verification.md)、[脱敏上游报告](http-api-p5-upstream-verification.json)。本轮真实文件写入、新分享生命周期和分享检查通过；同账号转存被百度明确拒绝，转存成功/下载子任务、离线、回收站及故障注入仅以受控证据核销。未调用真实账号全局清空。P6/P7仍为后续范围。
