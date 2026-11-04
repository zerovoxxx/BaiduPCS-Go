# HTTP API 服务启动与 P2 范围

当前版本：契约 `0.2.0-p2`。12 个 P1 系统/配置操作保持 implemented，另开放账号、单文件传输和任务控制的 10 个 partial 操作，其余 83 个仍 planned。配置快照、隔离 ClientFactory、Bearer 和 SQLite 沿用 P1；具体限制和 HTTP body 见[P2 操作指南](http-api-p2-guide.md)，证据见[P2 验证记录](http-api-p2-verification.md)，继续开发从[工程交接](http-api-handoff.md)进入 P3。

## 启动

从本仓库构建，Go 最低版本仍为 1.23.0，不需要 CGO。初期 server 支持 darwin/amd64、darwin/arm64、linux/amd64、linux/arm64 和 windows/amd64；原有 CLI 构建目标保留。

```sh
CGO_ENABLED=0 go build -o /tmp/baidupcs-go .
/tmp/baidupcs-go server --listen 127.0.0.1:5299 --data-dir /tmp/baidupcs-service
```

新数据目录会以 0700 创建；已存在的目录在 Unix 上也必须为 0700。首次未指定身份来源时，会产生：

| 文件 | 用途 |
| --- | --- |
| principals.json | 静态服务身份、scope、账号/root allowlist，以及 token 的 SHA-256 摘要 |
| client-token | 初始管理员的客户端凭据交接文件，0600；启动输出只给路径，不打印 token |
| master.key | 32 字节本地主密钥，0600；加密配置中包括代理秘密的持久化内容 |
| service.db、service.db-wal、service.db-shm | SQLite 元数据、配置历史、幂等记录和初始任务表 |
| service.lock | 数据目录独占锁；第二个服务实例不能调度或修改同一数据目录 |

后续启动从 `principals.json` 读取摘要，不需要再次读取交接文件。客户端取走凭据后可移动或删除 `client-token`；不会因此重置服务身份。备份和迁移时保留数据库及对应 `master.key`，不能为已有数据库重新生成密钥。

默认数据位置为 `os.UserConfigDir()/BaiduPCS-Go/http-service`。服务模式不调用旧 CLI 的配置初始化、不解释 `BAIDUPCS_GO_CONFIG_DIR`，也不切换进程 cwd。`server --help` 和 `help server` 同样不打开旧 CLI 配置。普通 CLI 命令、帮助、版本及交互式 shell 保持原初始化路径。服务需要从独立进程启动。

用自己的管理员 token 输入文件启动，也可避免生成交接文件：

```sh
/tmp/baidupcs-go server --data-dir /tmp/baidupcs-service --token-file /absolute/path/api-token
```

输入文件须为私有普通文件，内容为 32—512 字符的 Bearer token；末尾换行会移除。不要把 token 值放进命令行。`--token-file` 与启动文件中的 `principals` 二选一；两者都未指定时才使用或创建数据目录中的身份库。首次生成的 token 来自 crypto/rand 的 256 位随机值。

## HTTP 调用

以下 shell 从交接文件读取 token，token 只作为 Authorization 头发送。用实际路径替换示例值。

```sh
api_token=$(cat /tmp/baidupcs-service/client-token)
curl -H "Authorization: Bearer $api_token" http://127.0.0.1:5299/api/v1/system
curl -H "Authorization: Bearer $api_token" http://127.0.0.1:5299/api/v1/configuration
```

`GET /configuration` 返回强 ETag，例如 `"1"`。更新必须原样带上它；缺少 428，不匹配 412。同一 Idempotency-Key 的相同请求重放首次结果，不重复产生配置版本；请求摘要包括操作、If-Match 版本和规范化 body，改变输入返回 409。记录至少保留24小时，重启后仍可重放。重放复用逻辑结果、状态与ETag，request_id及X-Request-ID仍属于本次HTTP请求。

```sh
curl -X PATCH http://127.0.0.1:5299/api/v1/configuration \
  -H "Authorization: Bearer $api_token" \
  -H 'If-Match: "1"' \
  -H 'Idempotency-Key: config-example-1' \
  -H 'Content-Type: application/json' \
  --data '{"no_check":false,"max_download_rate":"0","proxy":""}'
```

false、0、空代理字符串会保留含义；null、重复 JSON key、未知字段、多个 JSON 值和错误类型会被拒绝。代理 URL 可以写入凭据，读取只返回 configured 与移除 userinfo 后的地址；响应、日志和配置数据库中的明文不会包含代理密码。配置存储按版本整体用 AES-GCM 加密，并以版本号绑定认证数据。

配置验证 `POST /configuration/validate` 不保存候选值；可解析但业务非法的候选返回 200 和 `valid=false`。重置使用 `POST /configuration/reset` 的非空 fields，不删除账号、任务或 API 身份。全部 22 个客户端字段、默认值和平台字节范围见 `/configuration/schema` 与 `/configuration/defaults`。

| 操作 | 当前范围 |
| --- | --- |
| GET /healthz、/readyz | 匿名最小状态；readyz 检查当前启用的存储/配置及默认 root，不要求百度在线 |
| GET /api/v1/system、/capabilities | 已认证；版本和全部 105 个操作的实际实现状态 |
| GET /api/v1/openapi.json、/docs | 已认证；内嵌契约与离线本地调试页，不加载外部 CDN |
| GET /api/v1/configuration | admin；版本化配置，代理脱敏 |
| PATCH /api/v1/configuration | admin；强 If-Match，支持幂等；全部字段仅用于后续新任务/客户端 |
| GET /api/v1/configuration/schema | admin；22 个客户端字段的类型、范围和生效方式 |
| POST /api/v1/configuration/validate | admin；候选校验，不写入 |
| POST /api/v1/configuration/reset | admin；强 If-Match，支持幂等 |
| GET /api/v1/configuration/defaults | admin；源码默认值与平台模板，默认 savedir 为 null |
| ACC01/02/03/07、UPL01、DWN01、JOB02/04/06/08 | P2受限账号、单文件与任务；必须遵守固定选项、权限和大小限制，详见P2指南 |
| 其他 planned 操作 | 先检查服务身份、scope 和路径账号 allowlist，随后 501 |

`/docs` 本身也需要 Authorization。可以通过能设置此头的客户端访问；调试页只向同源 `/api/v1/` 发出请求，token 不写浏览器存储。CORS 默认关闭，不采信未配置的反向代理身份头。日志只记录路由模板、operationId、状态、请求 ID 和耗时，不记录原 URL、请求体、Cookie 或 Authorization。服务使用结构化日志，不转发可能含上游签名的 CLI 原始 verbose 输出。

## 固定启动配置和权限

```json
{
  "listen": "127.0.0.1:5299",
  "data_dir": "./private/service",
  "roots": [
    {"id":"download_root","path":"/absolute/path/downloads","read":true,"write":true}
  ]
}
```

通过 `server --config /absolute/path/server.json` 加载；CLI 显式的 `--listen`、`--data-dir` 优先。配置文件里的 data_dir、TLS 路径相对配置文件目录解析，root.path 必须为绝对路径。root 需要实际存在，且不能包含服务数据、启动配置、API token 输入或 TLS 私钥，或位于这些私有路径内。未知 root、越界路径、断开的软链接和逃逸链接会被拒绝；本阶段校验目录，不创建传输目标或读写用户文件。

自定义身份时增加 `principals`，每项包含 id、token_sha256、scopes、accounts、roots；token_sha256 必须是自己 token 的 64 位十六进制 SHA-256。scope 只接受 read、write、transfer、local.read、local.write、admin。admin 可满足业务 scope，但账号/root allowlist 仍独立检查，`*` 需显式填写。配置更新接口只给 admin；普通身份无法通过修改请求 body 自行声明账号或权限。

TLS 使用启动字段 `tls_certificate`、`tls_key`，两者须同时给出，最低 TLS 1.2。身份、监听、TLS、数据目录和主密钥在进程启动时固定；CFG02 不修改这些字段，CFG06 重载按计划在 P6 实现。

## 生命周期与后续接入

进程入口处理 SIGINT/SIGTERM。关闭时先使 readiness 不就绪、停止接收新请求，HTTP 最多等待 10 秒排空，再取消任务并等待检查点/终态，随后关闭客户端、数据库和目录锁。任务关闭最多等待 15 秒，必要时取消网络后再有界等待 5 秒；未结束的执行器仍保留存储，错误会返回，不抢先关闭 DB。同步HTTP操作使用30秒context/读取/写入期限，服务没有统一的整体 WriteTimeout；SSE 和内容路径预留独立空闲策略，实际流与任务排空在 P3/P4 接入。

ClientFactory 是基础设施内部协作者，由 bootstrap 持有，供 P2 的百度适配器和 P4 的传输适配器使用。每次 New 明确给出 account_id、凭据 revision、配置 Snapshot 和 context，并在结束时 Close；返回全新 PCS、Cookie Jar、Transport、Resolver 和签名/缓存状态。更新配置或修改调用方原切片/凭据 map 不会改掉已绑定实例。新路径使用证书校验、精确代理主机规则和实例网卡配置，不回退到进程代理环境或 requester 全局设置。

同步请求保留30秒期限；P2传输绑定独立任务context，不使用整体30秒客户端超时。连接/响应头有界，流式body每次读取空闲超过30秒会取消；按任务取消和服务关闭到达实际连接。账号及传输已装配到server，queued记录持久扫描派发，未知提交结果核验后恢复。完整账号和文件读取在P3、完整传输在P4、CLI默认值管理在P6；事件、列表和逐项结果仍按完整阶段补齐。
