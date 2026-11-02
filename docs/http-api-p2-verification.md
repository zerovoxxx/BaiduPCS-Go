# P2 最小执行链路验证

日期：2026-11-02。状态：内部最小链路已实现并通过受控上游验证；P2整体尚未验收。服务对外仍只有P1的12个系统/配置操作，账号、上传下载及任务handler继续planned，未计入接口实现率。真实百度实测未运行。

## 调整与当前范围

依照[实施计划](http-api-implementation-plan.md)，先验证最容易迫使架构返工的账号、实际传输、取消及恢复边界。入口使用应用服务的内部组合调用，不增加验证专用公网路由；这份证据不等于完整HTTP端到端验收。

- accounts.Service规范化BDUSS/完整Cookie/access token，生成独立账号ID。凭据以AES-GCM保存，JSON账号对象不回显；当前不做身份在线校验，不能宣称已登录百度。
- transfers.Service绑定账号/配置/凭据版本，持久化后从服务生命周期执行；同逻辑提交不重复创建任务，错误输入复用key会拒绝。运行上限为2个任务、16个等待名额；同一服务实例拥有执行权。
- SingleFileEngine复用主项目FakeRapidUpload、UploadTmpFile、UploadCreateSuperFile和MultiUploader，验证真实预创建、multipart分片及合并流程。只开放内部单文件、skip策略和单文件并行；不复制CLI的目录扫描及全部秒传策略。
- 下载复用BaiduPCS.DownloadFile与requester/downloader新增ExecuteContext单连接入口，验证真实HTTP读取、context取消、偏移确认和严格Range。旧Execute及并行monitor保持原路径，不把它们标为已完成隔离。
- P1 SQLite增量迁移至schema v2，仅增加加密账号记录；复用现有jobs表保存内部执行记录与检查点。检查点的账号、目标、任务、配置及凭据版本绑定被核验。

## 实际证据

复现命令：

```sh
GOTOOLCHAIN=go1.23.12 go test -race -count=1 -timeout=90s \
  ./internal/domain/account ./internal/infrastructure/baidu
```

| 测试 | 证明的范围 |
| --- | --- |
| TestCredentialImportCanonicalization | 无尾分号Cookie不丢失；拒绝缺BDUSS、重复Cookie、换行、坏字段及混合凭据；账号JSON不含凭据 |
| TestSingleFileTransferRoundTripAndCancellationIsolation | 两账号同路径使用各自Cookie；上传后下载字节一致；取消A不影响B和新C；跨账号读取拒绝；取消任务不能恢复；同key去重及不同请求冲突；数据库/WAL无明文测试凭据 |
| TestSingleFileTransferRestartAndConfirmedRange | 停止并关闭存储，重建工厂与服务后恢复；注入遗留running状态转interrupted；已确认上传分片只发送一次；下载使用正偏移Range且最终字节一致 |
| TestSingleFileTransferRejectsChangedSourceAndCheckpointBinding | 更换目标、凭据版本或源文件时拒绝旧检查点，不能复用另一个目标的uploadid |
| TestSingleFileTransferReconcilesLostCommitResponse | 模拟合并已完成但响应连接丢失，保持interrupted；重开后按远端长度/摘要核对，合并操作总计只执行一次 |

测试只连接本机受控TLS服务，验证的是实际生产适配器、协议解析、执行组件和SQLite组合；没有访问百度，也没有读取用户正常账号或修改用户网盘。重启测试为组件关闭/重开及持久状态故障注入，尚未进行服务子进程SIGKILL、主机掉电或各目标机运行测试。

## 此次发现与处理

旧PCSUpload缓存进程级HTTP客户端，不能直接作为账号隔离适配器。本轮通过实例Session执行同样的百度协议，不复用该全局客户端。MultiUploader取消后等待provider返回再释放文件，并为并发终止错误赋值加锁。

旧Downloader取消可能返回nil，且monitor/缓存仍含共享状态。本轮增加独立、受限的ExecuteContext入口，使取消返回错误，检查点由调用方在文件Sync后确认；恢复要求206及精确Content-Range，不能把忽略Range的完整响应追加到文件。该入口不是对旧并行下载器已经安全的证明。

上传合并前保存committing状态，响应不明先核对；本地最终文件采用同目录临时文件与不覆盖目标的提交。取消与最终提交竞争时，不确定结果保持interrupted，不宣称取消撤销了已发生的效果。失败只持久化安全业务码，不保存含签名URL的原始错误文本。

## 限制及接下来的门槛

继续开发以[交接清单P2-01至P2-12](http-api-handoff.md#4-p2-剩余待办按这个顺序提交)为准；包括当前尚未落实的partial能力表达、HTTP装配、持久派发、关闭、错误映射及崩溃窗口。

当前内部验证支持1字节至32MiB普通文件、单连接下载、上传skip策略及无速率限制。已配置非零速率会明确拒绝；目录、空文件、并行下载、完整秒传/重试策略、暂存、流式代理和全部参数在P4完成。内部Record含加密存储所需配置和检查点，不是可直接序列化的HTTP响应。

下一步先接入受限HTTP DTO、原操作权限、脱敏响应、实际契约验证和runtime排空；只按已实现的选项开放能力。还须补齐同目标任务互斥、任务记录/检查点保留清理、凭据版本管理、子进程中断，以及显式专用账号的上游实测。P2门槛通过前不建设完整SSE、context CRUD或额外管理框架，也不把目前结果称为完整P2或完整上传下载。


## 本轮收尾验证

新增相关包及根入口的race通过，原14个离线CLI测试包通过。当前18组实际HTTP响应覆盖全部12个P1操作；105个操作、190个schema、67个边界用例和615条源码映射通过，公开实现率未因内部验证提高。调整后的源码重新完成13个CLI目标CGO=0构建，SQLite依然只在5个初始服务目标导入。P0记录的3项既有测试问题保留，未运行无筛选的全仓网络测试。
