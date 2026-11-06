# P6 发布记录

- 完成30项剩余业务/管理操作，契约0.6.0-p6，全契约104 implemented、0 partial、1 planned。
- 新增摘要/MD5修复/校验/导出/增量、兼容AES本地工具、完整HTTP密码/QR会话、owner上下文、固定来源配置重载、独立CLI默认值与诊断。
- 浏览器接收支持持久Upload-Offset、长度/摘要校验、重复complete、取消/配额/TTL，以及SIGKILL恢复。
- SQLite v7增量迁移保留既有任务/事件/检查点/回执/staging引用；密钥仅进程临时保留，重启明确不可恢复。
- INT06保留已实现的长期上游不可用语义；原AES无gzip格式的认证限制明确记录。
- 真实验证复用有效Cookie，专用目录摘要、校验、导出、上下文及PCS节点通过；修复请求被明确拒绝；登录交互与全局增量为受控证据，未要求重复扫码。
- P7继续SYS07指标、全量核销、备份/恢复和发布资料；不自动push或部署。

修复协议补充：原核心RapidUploadNoCheckDir会忽略XPan errno；P6改为同时核对errno/error_code，缺状态不算成功，明确拒绝不伪报成功。修正前的真实INT02成功结论已撤回，最终报告记录为upstream_rejected；中间只读网络超时也保留在prior_attempts。
