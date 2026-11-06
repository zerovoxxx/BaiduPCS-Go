# BaiduPCS-Go

> 项目导航入口。需求、设计与验收以 `docs/astack/version/*_SPEC.md` 为准。

<!-- 维护原则：只写读代码读不出来、写错会出事的内容；每条规则可检查，能用 lint / 测试 / hook 强制的不要只靠这里；同一件事只写一处，其他章节按名称引用；架构地图、项目红线始终保留章节，没有内容时留空；其余没有内容的章节整节省略，不留空标题。各章写法见 harness-init 的 references/claude-md-guide.md。 -->

## 项目画像

仿 Linux shell 文件处理命令的百度网盘命令行客户端，支持多账号、扫码登录、文件上传下载、分享和转存。

## 命令速查

| 质量门 | 命令 | 何时跑 |
|---|---|---|
| 静态检查 | `go vet ./internal/pcslogin ./internal/pcsconfig ./internal/pcscommand ./requester` | 登录、配置、命令或 HTTP 层变更；其他模块按影响面调整包路径 |
| 聚焦测试 | `go test ./internal/pcslogin ./internal/pcsconfig ./internal/pcscommand ./requester` | 登录与会话相关变更；其他模块选择对应包，上传任务用 `go test ./internal/pcsfunctions/pcsupload` |
| 完整测试 | `go test ./...` | 跨模块契约、共享代码或高风险变更；结果需结合下方历史基线判断 |
| 构建 | `CGO_ENABLED=0 go build -trimpath -ldflags '-w -s' -o /tmp/BaiduPCS-Go .` | 产品代码或依赖变更；参数依据 `.github/workflows/main.yml`，产物放仓库外 |
| HTTP 服务 | 按 `.github/workflows/http-api.yml` 执行 race、14 组实际响应契约、CLI 回归、恢复演练和平台构建 | HTTP 或共享客户端变更；命令和范围见 `asset/http-api-p7-verification.md` |
| 文档 / Harness | `git diff --check`；`python3 .claude/skills/spec/scripts/spec_workflow.py list --root . --all` | 文档、治理入口或 SPEC / PLAN 变更 |

CLI 构建 CI（`.github/workflows/main.yml`）的 Go 版本取自 `go.mod`；HTTP CI（`.github/workflows/http-api.yml`）另行执行服务测试与契约检查。上述 CLI 聚焦检查的历史证据见「知识导航」中的原生扫码验证记录；全量测试的历史问题包括 `requester/downloader` 测试引用不存在的 `Monitor.ShowWorkers`、`pcsutil` 和 `pcsutil/checksum` 依赖开发者绝对路径，以及 `pcsutil/getip` 依赖外网服务。验证时分别报告本次结果与历史问题，不把聚焦检查通过写成全量通过。

## 架构地图

### 技术栈与工程边界

- `go.mod` 声明 Go 1.23，模块路径为 `github.com/qjfoidnh/BaiduPCS-Go`；这是跨平台 Go 命令行项目，CLI 使用 `urfave/cli` v1。
- `main.go` 注册命令并接入 `internal/pcscommand`；不带参数进入 `pcsliner` 交互模式。`internal/pcsfunctions` 编排传输任务，`baidupcs` 封装网盘接口，`requester` 提供 HTTP、CookieJar 和传输组件。

### 业务子领域与边界

- 扫码登录分为 `internal/pcslogin` 的协议与二维码处理、`internal/pcscommand/qrlogin.go` 的展示和取消，以及 `internal/pcsconfig` 的账号验证与持久化；协议细节见「知识导航」中的扫码登录方案。
- 账号和客户端配置存于 `pcs_config.json`，由 `BAIDUPCS_GO_CONFIG_DIR` 指定目录；未指定时先兼容可执行文件旁的旧配置，再使用 Windows `%APPDATA%/BaiduPCS-Go` 或其他系统 `$HOME/.config/BaiduPCS-Go`，具体回退逻辑以 `GetConfigDir` 为准。

## 项目红线

- [禁止] 将扫码状态返回的临时票据直接保存为正式 BDUSS；正式登录需完成会话交换、身份与网盘访问验证及配置持久化（依据 `asset/qr-login-design.md`）。
- [禁止] 扫码登录链路降级为 HTTP、关闭证书校验，或将登录重定向放宽到未经允许的域（依据同上）。
- [禁止] 在日志或验证记录中写入真实 Cookie 值、临时票据、完整认证 URL 或原始登录响应（依据同上）。

## 项目约定（特殊约定）

- 错误处理：失败要么就地处理，要么带着原始错误向上传递；不吞错误（空 catch、忽略返回的 error、静默回退）。
- 依赖：不引入新的第三方依赖或测试框架，除非需求要求；确需引入时在汇报中说明理由，并同步锁文件。

### 领域实现

- 账号的 `cookie_entries` 缺失或为 `null` 时走旧凭证恢复；非 `null` 数组走结构化 Cookie 恢复，空数组表示会话已清空。此兼容语义及 Cookie 的域、路径和 Secure 属性见扫码登录方案。
- CLI 运行会初始化账号配置。手动验证使用独立配置目录；`TestLiveSavedQRSession` 只有设置 `BAIDUPCS_QR_LIVE_CONFIG` 指向已登录的隔离配置文件才启用，默认跳过不代表真实账号验证通过。

## 知识导航

- [README.md](README.md)：用户命令、配置项与跨平台编译说明；修改 CLI 行为前读对应章节。
- [asset/README.md](asset/README.md)：百度 PCS API、错误码与客户端功能文档索引；排查网盘接口前定位对应资料。
- [扫码登录方案](asset/qr-login-design.md)：协议、账号兼容语义、持久化与退出码；修改登录或会话恢复前读。
- [原生扫码验证记录](asset/qr-login-native-verification-2026-11-02.json)：2026-11-02 的检查范围、平台结果和全量测试历史问题，用作基线参考。
- [HTTP 服务验收](asset/http-api-p7-verification.md)：P1—P7 的实现、契约、离线测试、真实上游范围及 CI 复现入口；修改服务或共享客户端前读。
- [harness-init](../astack/astack-marketplace/plugins/astack-coding/skills/harness-init/SKILL.md)：本次初始化的技能来源；修复或升级 Harness 时使用，其他技能按具体任务选用。

<!-- astack:workflow:start -->
## AI 协作规范

<!-- 本节由 Harness 维护，升级时整体替换；项目内容请写在上面的章节里。 -->

### 开发原则

- **执行边界**：授权以用户请求为准并持续有效，只读检索、本地草稿、可恢复的修改和常规本地检查直接推进，实现细节按代码证据自行决定；缺失的业务事实可一次性询问，同时继续不依赖它的工作。只在「项目红线」中标 `[须授权]` 的动作（如有），或其他不可逆、难以恢复且尚未获得授权的动作前暂停：不执行该动作，其余工作照常，备好 diff / dry-run 与回退方案并请求授权，未获答复不等于授权；提交、推送、发布只在用户请求的范围内执行。
- **验证与读取**：不删除、弱化或跳过测试 / 断言来让检查变绿，原断言本身错误时才修改并在汇报中说明；从目标文件和相关测试读起，按引用链扩展，不通读全仓。
- 工作类型以用户明确指令为准；未说明时按项目现状推断并说明依据，推断不出时询问，不猜测。专项 skill 按任务需要自动选用，用户指定或排除优先；独立专项任务直接处理。目标和改法都清楚的小改动，以及不用排查就能确认的小问题，直接实施；其余产品变更先走 spec：判定新项目、新功能、缺陷或重构，产出对应方案，再由 dev 实施。技术栈、架构边界、持久数据、对外契约等难以回退的决策，定稿前由用户确认，用户说“你定”即授权。入口内容仅在新项目架构设计阶段自动回写；其余须用户明确决策并要求写入。
- SPEC：`docs/astack/version/<ID>_<PascalSlug>_SPEC.md`；PLAN：`docs/astack/plan/<ID>_<PascalSlug>_PLAN.md`，共用 ID / Slug。新 ID 用 spec skill 的 `python3 scripts/spec_workflow.py id` 生成；用 `python3 scripts/spec_workflow.py list --root <项目根目录>` 按需定位（`--active` 只列未完成，`--all` 含归档），不遍历历史正文。

### 编程哲学

#### 🚀 编码哲学 (Karpathy-Inspired Coding Guidelines)

> "Tradeoff: These guidelines bias toward caution over speed. For trivial tasks, use judgment."

##### Think Before Coding
- **State your assumptions explicitly.** If uncertain, ask.
- **Don't assume. Don't hide confusion. Surface tradeoffs.**
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- **If something is unclear, stop. Name what's confusing. Ask.**

##### Simplicity First
- **Minimum code that solves the problem. Nothing speculative.**
- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- **If you write 200 lines and it could be 50, rewrite it.**

##### Surgical Edits
- **Touch only what you must. Clean up only your own mess.**
- Avoid "while I'm here" refactoring of unrelated code.
- Keep diffs small and focused.
- If a change requires touching 10 files, explain why before doing it.
- Don't reformat code unless it's the primary task.

##### Goal-Driven Execution
- **Define success criteria. Loop until verified.**
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make them pass"
- "Refactor X" → "Ensure tests pass before and after"
- **Validation is the only path to finality.**
<!-- astack:workflow:end -->
