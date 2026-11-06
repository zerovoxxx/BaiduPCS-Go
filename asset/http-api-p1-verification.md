# HTTP API P1 实施与验证

日期：2026-11-02。本轮将配置快照、隔离 ClientFactory、Bearer 鉴权、服务启动及 12 个 P1 系统/配置操作落到生产代码。契约版本 `0.1.0-p1`，105 个操作中 12 个 implemented、93 个 planned；没有调用真实百度账号或网盘文件。

## 五层落地

| 层 | 代码 | 结果 |
| --- | --- | --- |
| 入口组装 | main.go、internal/bootstrap | server 参数、模式判定、显式 CLI 初始化、signal.NotifyContext、资源装配与关闭 |
| HTTP 接入 | internal/httpapi | 路由/DTO、Bearer 协议、强 ETag、严格 JSON、Problem、请求 ID、日志和期限 |
| 应用服务 | internal/application/auth、configuration、system | 权限、候选配置、版本/幂等编排、系统信息和能力 |
| 领域与 ports | internal/domain、internal/ports | 拥有数据的 Snapshot、账号凭据、principal、错误、配置仓库/身份/本地与网络环境边界 |
| 基础设施 | internal/infrastructure | 独立百度客户端、SHA-256 身份库、SQLite/AES-GCM、目录锁、受限 root、网卡查询 |

应用/领域/HTTP 对依赖方向的约束由 `TestLayerDependencyDirection` 检查。ClientFactory 当前由基础设施适配器使用，不让应用服务依赖 baidupcs 或返回 Go 客户端对象；账号/文件用例在 P2 通过新的业务 ports 接入。

## 关键保证

- Snapshot 构建和读取均复制切片与目录引用，默认值、配置历史和客户端不共享可变字段。显式 false、速率 0 和空代理字符串可生效。
- 配置更新、版本和幂等记录同事务提交；ETag 竞争只成功一个，相同幂等请求的并发重放不重复写入，重启后仍可重放首次版本。
- ClientFactory 不调用 ActiveUser/CopyPCS，不读取 requester 全局代理/UA/网卡/DNS缓存；每次绑定凭据和配置版本，Cookie/缓存/签名/Transport 分实例。上游使用 TLS 验证、受控主机与逐跳重定向检查，context 取消到达实际连接。
- Bearer 与百度凭据独立，SHA-256 摘要恒定时间比较；权限与账号/root allowlist 分开。普通配置接口不接受账号、凭据数组或启动字段。
- 服务模式绕开 CLI 的 cwd/配置副作用，包括包初始化阶段的 GetConfigDir。旧 CLI 的默认路径选择、配置加载、非法配置退出和帮助/版本流程保留。
- 服务数据目录独占、密钥与身份私有；配置连同代理秘密加密存储；响应和结构化日志不包含输入秘密。
- SQLite driver 只在五个初始 server 目标导入，其余原 CLI 目标保留命令并可构建；在不支持目标执行 server 会明确拒绝。

## 验证与复现

Go 基线仍为 1.23.0，本轮用 Go 1.23.12 验证。`go.mod` 没有额外 toolchain 指令。加入固定的 modernc.org/sqlite v1.38.2 及其依赖；MVS 将 x/sys 升到 v0.34.0、x/sync 升到 v0.15.0，原 CLI 回归和构建矩阵均重新验证。

```sh
BAIDUPCS_P1_CONTRACT_RESPONSES=/tmp/baidupcs-p1-responses.json \
GOTOOLCHAIN=go1.23.12 go test -race -count=1 -timeout=120s \
  ./internal/domain/... ./internal/application/... \
  ./internal/infrastructure/... ./internal/httpapi ./internal/bootstrap .

python3 api/openapi/tools/export_contract.py
/tmp/baidupcs-http-p0-venv/bin/python api/openapi/tools/check_contract.py \
  --live-responses /tmp/baidupcs-p1-responses.json

python3 api/openapi/tools/check_builds.py --report /tmp/baidupcs-p1-builds.json
```

| 验证 | 结果与实际范围 |
| --- | --- |
| 新增测试与 race | 通过；快照隔离、两个账号并发本地 TLS 上游、代理/UA/凭据版本、取消与不安全重定向、root/软链接、权限、配置/CAS/幂等、重启、目录锁、关闭和 CLI 子进程 |
| 实际 HTTP 契约 | 18 组实际响应覆盖全部 12 个已实现操作以及 400/401/403/412/428/501 错误；schema、媒体类型、request_id、服务内嵌文档一致 |
| P0 离线契约 | 105 个操作、190 个 schema、67 个正反边界用例和 105 个示例 exchanges 均通过；25 组后续行为规格仍按阶段核销 |
| P1 收尾 AST | 614 条逐项映射；35 个顶层命令、52 个节点、90 个命令参数；原 132 个 baidupcs 导出入口不变 |
| 旧 CLI 离线回归 | 原 14 个包全部通过，命令见下文；P0 记录的 3 项既有问题未改动 |
| CLI 构建 | .github 矩阵与 build.sh 的 13 个目标并集均 CGO=0 编译通过；依赖检查证实只有 5 个服务目标导入 SQLite |
| 原生进程启动/退出/重启 | darwin/arm64：旧 CLI 配置为损坏 JSON 时仍可启动 server，修改配置后 SIGTERM 退出为 0，重启保留版本及 false，旧文件字节不变 |
| 真实百度实测 | 未运行；本轮两个账号的网络验收仅连接本地受控 TLS 服务 |

旧 CLI 回归命令保持明确的离线集合：

```sh
GOTOOLCHAIN=go1.23.12 go test -count=1 -timeout=120s \
  ./pcsliner/args ./pcsutil/cachepool ./pcsutil/converter \
  ./pcsutil/escaper ./pcsutil/waitgroup ./pcsutil/taskframework \
  ./baidupcs/netdisksign ./baidupcs/expires/cachemap \
  ./requester ./requester/rio ./requester/rio/speeds \
  ./requester/uploader ./internal/pcscommand ./internal/pcsfunctions/pcsupload
```

| 目标 | CLI 编译 | SQLite 导入 |
| --- | --- | --- |
| darwin/amd64、darwin/arm64 | 通过 | 是；支持 server |
| linux/amd64、linux/arm64 | 通过 | 是；支持 server |
| windows/amd64 | 通过 | 是；支持 server |
| linux/386、linux/arm | 通过 | 否；CLI 保留 |
| linux/mips、linux/mipsle | 通过（softfloat） | 否；CLI 保留 |
| linux/mips64、linux/mips64le | 通过 | 否；CLI 保留 |
| windows/386、windows/arm | 通过 | 否；CLI 保留 |

除 darwin/arm64 的实际运行外，其他平台只完成交叉编译，未声称目标机运行验收通过。未运行不加筛选的 go test ./...；下载器旧测试的 ShowWorkers 编译问题及两个开发者绝对路径测试仍属于 [P0 基线](http-api-p0-baseline.md)。

## 契约与范围

YAML 仍是唯一契约源；openapi.json 由 export_contract.py 生成并 go:embed，校验器核对二者。服务启动会核对 capability 声明与实际 handler，防止把占位操作标 implemented。已实现操作附有离线测试证据；其余操作授权后 501。405/Allow 明确纳入公共错误响应，HEAD 的错误也无 body。

当前 source-inventory.json 随代码更新；P1收尾清单保存在baselines/p1-source-inventory.json；[P0 冻结清单](../api/openapi/baselines/p0-source-inventory.json)保留当时的 609 条与原源码哈希。新增分层内部函数按目录责任、依赖和行为验证管理，不为内部对象制造公网入口。

P2 才实现账号仓库、规范化 Cookie 导入/吊销、context、CLI 默认值和文件读取；P3 才接持久任务执行/事件；P4 才接完整传输与流式客户端。P1 不改变旧上传/下载执行流程，也不把原全局任务控制当作新的按任务控制。


## 收尾与后续边界

2026-11-02在调整设计顺序后重新运行P1相关race及CLI子进程测试，通过；18组实际HTTP响应、105个操作、190个schema、67个边界用例和614条P1源码映射通过。13目标构建报告已检查：所有exit_code/dependency_exit_code为0，SQLite只在5个初始服务目标链接。重复构建不作为进度，只有后续代码变化影响结论时重新运行。

保留本阶段全部成果，不再增加管理框架。P2最小执行链路单列在[记录](http-api-p2-verification.md)；不会将账号、传输或流式代理标成P1已支持。
