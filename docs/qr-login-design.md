# BaiduPCS-Go 百度网盘官方 App 扫码登录技术方案

日期：2026-11-02。范围：qjfoidnh/BaiduPCS-Go 命令行项目。

目标是让用户执行 `BaiduPCS-Go qrlogin`，使用百度网盘官方 App 扫描二维码并确认，程序自动取得登录会话、验证网盘访问、保存账号，后续命令和程序重启均能复用登录状态。

方案采用百度网页登录的二维码授权流程。2026-11-02 的现场验证已通过官方 App 扫码确认、正式会话交换、网盘读取、CookieJar 恢复和现有 CLI 隔离导入。这证明该账号和当前环境下的链路可用，正式的 `qrlogin` 命令尚未实现。接口属于网页登录内部协议，需要把协议适配与账号配置分开维护。

机器可读的结论、返回码、Cookie 名称及作用域保存在[脱敏验证记录](qr-login-verification-2026-11-02.json)。该记录不包含凭证值、账号标识、文件名或二维码载荷。

## 用户操作

```bash
BaiduPCS-Go qrlogin
BaiduPCS-Go qrlogin --timeout 300
BaiduPCS-Go qrlogin --qr-file /tmp/baidu-login.png
```

终端显示二维码和当前状态，图片文件作为终端显示不清晰时的备用方式。用户在官方 App 中完成扫码和确认后，终端显示已登录账号。凭证获取、导入和保存均由程序完成。

默认总超时建议为 300 秒，以服务端的二维码有效期为上限。二维码过期后提示重新生成；新二维码对应新会话，不沿用旧临时票据。Ctrl+C 取消当前会话，释放请求并清理二维码临时文件。

## 授权和凭证流程

| 步骤 | 请求或操作 | 成功条件 |
| --- | --- | --- |
| 创建会话 | 独立 HTTP Client 和 CookieJar，生成本次会话的 gid | 使用 HTTPS，并保留首次响应下发的会话 Cookie |
| 获取二维码 | `passport.baidu.com/v2/api/getqrcode`，携带 `lp=pc`、`qrloginfrom=pc`、`tpl=netdisk`、`apiver=v3` 等参数 | 返回 `errno=0`、`sign` 和 `imgurl` |
| 获取图片 | 在同一会话中请求官方 `imgurl` | 图片可解码，并可被百度网盘官方 App 识别 |
| 等待授权 | `passport.baidu.com/channel/unicast`，以 `channel_id=sign` 轮询 | 用户在官方 App 确认后，收到成功状态及临时票据 |
| 交换会话 | `passport.baidu.com/v3/login/main/qrbdusslogin`，以临时票据完成登录交换 | 接收正式登录 Cookie，处理必要的百度域内重定向和会话同步 |
| 验证网盘 | 使用得到的会话访问网盘容量、账号信息和最小目录列表 | 接口返回成功，并符合预期响应结构 |
| 导入账号 | 校验身份、更新同账号记录、设置当前账号并持久化 | 保存成功，新进程恢复后仍能读取网盘 |

整个扫码过程使用同一 CookieJar，保留 Cookie 的域、路径、过期时间和 Secure 属性。扫码状态接口返回的临时票据必须继续交换，不能直接存为正式 BDUSS。正式凭证以服务端下发的 Cookie 为依据，不以票据长度或响应正文出现了 `bduss` 字段判断登录成功。

Passport 域和网盘域可能出现同名 Cookie。应完成网盘域需要的会话同步，再按目标请求 URL 选择 Cookie，避免无条件合并或覆盖不同域的 STOKEN。BAIDUID 等会话字段与 BDUSS 一起保留；BDSTOKEN 和 STOKEN 是不同字段。

二维码展示以官方图片为来源。若使用二维码库在终端重绘，应先解码图片的实际载荷，再保持载荷不变地渲染。`imgurl` 是图片下载地址，不能把该地址重新编码后当成登录二维码。

## 本次通过的请求参数

以下参数来自本轮成功验证的请求，供正式实现复现。它们是本次观测到的组合，不表示每个字段都已证明必需。动态字段仅保留生成规则，不保留实际值。

| 请求 | 本次使用的参数 |
| --- | --- |
| getqrcode | `lp=pc`、`qrloginfrom=pc`、`tpl=netdisk`、`apiver=v3`、`gid=<本次会话 UUID>`、`callback=bd__cbs__qrcreate`、`tt=<毫秒时间戳>`、`_=<毫秒时间戳>`、`logPage=traceId:pc_loginv4,logPage:loginv4` |
| channel/unicast | `channel_id=<sign>`、同一 `gid`、`tpl=netdisk`、`apiver=v3`、`_sdkFrom=1`、`callback=bd__cbs__qrpoll`、`tt` 和 `_` 毫秒时间戳 |
| qrbdusslogin | `bduss=<扫码临时票据>`、同一 `gid`、`u=https://pan.baidu.com/disk/main`、`loginVersion=v4`、`qrcode=1`、`tpl=netdisk`、`apiver=v3`、`alg=v3`、`callback=bd__cbs__login`、`traceid` 为空、`v` 和 `tt` 为毫秒时间戳、`time` 为秒时间戳 |
| 网盘会话同步 | 先访问 `pan.baidu.com/disk/main`，再访问 `passport.baidu.com/v3/login/api/auth/`，后者带 `return_type=5`、`tpl=netdisk`、`u=https://pan.baidu.com/disk/main` |
| 最小目录读取 | `dir=/`、`num=1`、`page=1`、`order=name`、`desc=0`；本次请求还带 `channel=chunlei`、`web=1`、`clienttype=0`、`app_id=250528` |
| PCS 容量读取 | `pcs.baidu.com/rest/2.0/pcs/quota`，使用 `method=info`、`app_id=266719` |

请求参数通过 HTTP 库编码，`u` 在代码中使用原始 URL，避免人为重复编码。扫码链路全程使用一致的浏览器 User-Agent 和同一 CookieJar。本次脚本使用 Chrome 风格的 Linux User-Agent，脚本本身运行在 macOS。

轮询在等待阶段观测到 `errno=1` 且没有 `channel_v`；手机确认后观测到 `errno=0`，`channel_v` 是 JSON 字符串，解析后 `status=0` 且 `v` 非空。该次返回中的 `v` 继续用于凭证交换。本次没有采集拒绝授权、过期和额外验证的状态码，正式实现不能凭猜测补齐这些映射。

## 模块和接口设计

新增内部扫码协议模块，负责请求、响应解析和会话状态；命令层负责展示、取消、账号导入和错误退出。协议模块接受可注入的 HTTP Client，便于用本地测试服务覆盖网络状态。

建议的职责划分：

```text
CreateChallenge(ctx)       获取官方图片和本次会话标识
WaitForConfirmation(ctx)   等待扫码确认并返回临时票据
ExchangeSession(ctx)      交换正式 Cookie 并完成网盘会话同步
VerifySession(ctx)        校验账号和网盘读取
ImportAndSaveAccount()    更新账号并持久化
```

临时票据和正式凭证只在会话对象内部传递。状态回调仅包含状态、可展示的错误码和二维码图片，不携带 Cookie、完整请求 URL 或票据。JSON 和 JSONP 使用明确的解析器处理；不执行服务端返回的 JavaScript。

本地状态依次为创建中、等待扫码、等待手机确认、交换凭证、验证账号、保存配置和完成。取消、超时、二维码过期、额外验证、网络失败、协议变化和保存失败分别返回可识别的错误。服务端数字状态码的含义需通过返回数据和手机提示核对，未知状态不得被当成成功。

## 项目改造点

| 位置 | 改造内容 | 原因 |
| --- | --- | --- |
| [main.go](../main.go) | 新增独立 `qrlogin` 命令、超时和二维码文件选项，正确传播取消与错误退出 | 现有 `login` 只处理密码和导入凭证 |
| 新增内部扫码模块 | 实现挑战创建、图片处理、状态轮询和登录交换 | 将可变化的网页登录协议集中维护 |
| [internal/pcsconfig/maniper.go](../internal/pcsconfig/maniper.go) | 校验扫码凭证和账号身份后导入，稳健解析旧 Cookie 字符串 | 现有 BDUSS 正则假定字段存在且以分号结束，缺失时可能越界 |
| [internal/pcsconfig/baidu.go](../internal/pcsconfig/baidu.go) | 从完整会话恢复业务客户端，明确定义结构化 Cookie 和旧字段的优先级 | 当前 STOKEN 非空时，不会从 COOKIES 恢复其他会话字段 |
| [requester/http_client.go](../requester/http_client.go) 或扫码专用客户端 | 登录请求启用证书校验，兼容既有代理规则，支持 context 取消 | 当前默认 Transport 跳过 TLS 证书校验，Req 未接收 context |
| 配置保存和命令错误处理 | 保存成功后才报告成功；失败时保留已有账号 | 当前 saveFunc 只打印保存错误；main 未显式处理 app.Run 的返回值 |
| README 和测试 | 说明官方 App 操作、二维码过期处理、退出码与恢复行为 | 将可用性与接口存在区分开 |

账号当前有 BDUSS、STOKEN、BAIDUID、COOKIES 等字段，可用于兼容导入。但 Cookie 字符串无法完整表达域和路径；正式实现建议增加可选 `cookie_entries` 字段，保存 name、value、domain、path、secure、expires 等恢复所需属性。新版本优先恢复结构化 Cookie，再按明确定义的规则兼容旧配置。

恢复完成的 CookieJar 可通过现有 `NewPCSWithClient` 接入业务客户端。现有部分 Cookie 读取使用 HTTP 形式的 URL，而带 Secure 属性的 Cookie 只适用于 HTTPS；接入结构化 Cookie 时，需要同时校正这些查询 URL，验证获取 BDUSS、BAIDUID 和后续 Cookie 更新不会遗漏或降级。

账号身份不能仅由“存在 BDUSS”推断。现有导入通过 baidu-tools 的用户信息接口确定 UID；该路径在本次隔离导入中已通过，但依赖库目前向 `http://tieba.baidu.com/c/s/login` 发送认证请求。正式集成需替换为通过验证的 HTTPS 身份校验路径，保持与现有账号 UID 的语义一致，不能未经核对直接用网盘 UK 替代 UID，也不能使用伪造 UID 覆盖账号。

## 网络和本机数据处理

登录请求使用 HTTPS 和证书校验。自动跟随的登录重定向限制在允许的百度域，禁止降级为 HTTP；代理设置沿用已有配置。每个请求有独立超时，轮询另有总期限；长轮询的读取超时不等同于二维码过期。

日志只记录阶段、HTTP 状态、业务错误码、Cookie 名称和字段存在性，不记录 Cookie 值、临时票据、完整认证 URL、原始登录响应或目录内容。配置目录和凭证文件按项目现有习惯使用 0700 和 0600 权限。

同账号重新登录应保留有效的用户设置；新会话验证和持久化完成前，不删除旧会话。正式实现应采用可恢复的写入方式，避免配置写入中断留下截断文件。额外验证交给百度官方页面或 App 完成，只有后续接口验证通过才报告成功。

## 验证方案

验证分为协议试验和正式功能验收。协议试验使用独立脚本、独立 CookieJar 和临时账号配置，读取最少的业务数据。项目 Go 业务源码和用户现有账号配置不参与修改。

本次现场验证按以下步骤完成：

1. 从官方接口获取二维码，将图片展示在聊天中。
2. 用户使用百度网盘官方 App 扫描，并在手机上确认登录。
3. 验证程序自动交换正式登录 Cookie，仅输出脱敏阶段信息。
4. 读取网盘容量和根目录一条记录；记录返回码与结构，不输出文件名。
5. 保存并重新加载独立 CookieJar，重复最小目录读取，检查会话能否恢复。
6. 使用当前 BaiduPCS-Go 构建产物和隔离配置自动导入 Cookie，并通过新进程的 who、quota 检查现有 CLI 兼容性。

用户无需提供密码、验证码或复制 Cookie。二维码过期、手机拒绝或出现额外验证时，以手机提示和服务端状态确定后续操作。

正式功能还需覆盖：扫码取消和超时、过期后重建会话、网络短暂中断、JSONP 异常、缺少必要凭证、同名 Cookie 的域差异、证书错误、账号识别失败、配置保存失败、多账号切换和旧配置兼容。小文件上传下载及依赖 STOKEN 的业务能力单列实测，本次只读验证不代表全部网盘功能通过。

## 实施顺序和完成标准

第一阶段完成官方 App 扫码、票据交换和最小网盘读取的现场试验，确定实际返回结构、Cookie 作用域和额外验证行为。

第二阶段实现 CLI 与协议模块，接入完整凭证导入和保存，补齐取消、超时和错误传播。二维码展示优先选择纯 Go 实现，保持现有 CGO_ENABLED=0 的跨平台构建方式。

第三阶段完成会话恢复、旧配置兼容和异常测试，并进行 Linux amd64、Linux arm64 和当前开发平台的构建检查。

验收要求：官方 App 能识别二维码并完成确认；正式凭证自动取得；必要网盘读取通过；重启后无需重新复制 Cookie；失败不会覆盖已有账号；配置保存失败返回失败；日志和交付文档不含登录凭证。一次扫码成功只证明该账号和环境的链路可用，不据此承诺所有账号或将来协议版本均可用。

## 现场验证记录

验证环境为 macOS arm64，使用独立 Python 协议脚本和当前 Go 源码的本机构建产物。Linux 上的运行与交叉编译检查属于后续正式实现验收，本次未执行。

验证记录时间为 2026-11-02 16:03:32，时区为 Asia/Shanghai；源码基线为 `bf76d65b2f37e0e4460d836ca75b76246f4d06dc`。

| 验证项 | 当前结果 | 证据或判定方式 |
| --- | --- | --- |
| 官方二维码签发 | 通过 | getqrcode 返回 errno 0 |
| 官方二维码图片 | 通过 | HTTP 200，Content-Type 为 image/png |
| 官方 App 识别和确认 | 通过 | 用户确认使用官方 App 完成登录；轮询返回 errno 0、channel_v.status 0 |
| 正式 Cookie 交换 | 通过 | qrbdusslogin 返回 HTTP 200；网盘请求可用的 BDUSS、STOKEN、BAIDUID 均存在 |
| 网盘容量 | 通过 | pan 的 api/quota 返回 HTTP 200、errno 0 |
| 最小目录读取 | 通过 | api/list 返回 HTTP 200、errno 0，num 设为 1；没有输出文件名 |
| 网盘身份读取 | 通过 | api/user/getinfo 返回 HTTP 200、errno 0，符合账号记录结构 |
| CookieJar 重新加载 | 通过 | 持久化后创建新客户端，再次读取最小目录成功 |
| 当前 CLI 隔离导入 | 通过 | 自动传入 Cookie，现有 login 显示成功，隔离配置中有账号记录 |
| 当前 CLI 账号恢复 | 通过 | 新进程执行 who，恢复到非零 UID |
| 当前 CLI 容量读取 | 通过 | 新进程执行 quota，返回正常容量输出 |
| PCS 容量接口参数核对 | 通过 | 使用项目默认 app_id 266719 返回合法 quota、used 结构 |

验证发现以下实现约束：

1. 登录交换后先后访问网盘落地页和 Passport 的网盘认证同步入口，网盘读取成功。本次未单独比较移除任一步骤的行为，因此正式实现应先保留该顺序，再通过独立试验确定能否简化。
2. 实际会话同时包含 `.passport.baidu.com` 和 `.pan.baidu.com` 域下的 STOKEN，二者均标记为 Secure。PTOKEN 出现在 Passport 域，不能因为网盘请求不携带 PTOKEN 就判定登录失败。
3. 直接探测 PCS 容量接口时，app_id 250528 返回 HTTP 400、错误码 31030；改用项目默认的 266719 后成功，现有 CLI 的 quota 也成功。登录验证请求应按各业务接口沿用项目参数，不能统一套用网页端 app_id。
4. 现有 CLI 的隔离导入证明了账号兼容性，但沿用了依赖库的 HTTP 身份校验和现有 Transport 的 TLS 配置。扫码脚本的授权请求使用 HTTPS 和证书校验；正式实现仍需解决 CLI 这两处已有网络安全问题。

本次结果仅涵盖该账号、当前网络与只读接口。上传下载、转存、分享写操作、多账号并发、拒绝授权、额外验证和长期会话有效性不在本轮通过结论内。验证使用独立临时目录，默认账号配置未被导入或切换；交付记录不包含账号名称、文件名或 Cookie 值。

## 本机数据清理记录

2026-11-02 16:08:12（Asia/Shanghai）复查并清理本次验证数据。保留上述验证结论、请求参数、返回码和 Cookie 作用域，不保留真实 Cookie、凭证或挑战标识。

| 检查对象 | 清理和复查结果 |
| --- | --- |
| 本轮私有临时目录 | 已删除整个目录，复查不存在 |
| 独立 CookieJar 文件 | 随临时目录删除；没有保留 session.json 或其副本 |
| CLI 隔离账号配置 | 随临时目录删除，包括 pcs_config.json 中的用户登录态 |
| 扫码临时票据和挑战文件 | temporary-ticket.txt、challenge.json 已随目录删除，没有保留实际 gid、sign 或票据值 |
| 登录交换原始响应和 CLI 原始日志 | exchange-response.html 和 cli 日志已随目录删除 |
| 已使用的二维码图片 | qr.png 已删除；未保留二维码载荷 |
| 验证进程 | 本轮验证脚本及其 CLI 子进程已结束 |
| 默认 CLI 配置和项目目录配置 | 默认 ~/.config/BaiduPCS-Go/pcs_config.json 与项目目录 pcs_config.json 均不存在 |
| 本轮项目及交付目录 | 未发现上述敏感文件残留；保留文件的凭证值模式扫描无命中 |

仅保留技术方案、脱敏 JSON 记录和不含真实凭证的验证脚本。扫描检查用于确认交付内容没有意外写入凭证，清理结论同时依据本轮写入位置和目录删除结果。验证没有使用浏览器登录，也没有向系统钥匙串保存凭证；本次执行的是本机数据清理，没有发起百度服务端注销或操作手机 App 的登录状态。

## 协议和代码参考

- [BaiduPCS-Go 登录说明](https://github.com/qjfoidnh/BaiduPCS-Go#登录百度帐号)
- [扫码流程参考实现](https://github.com/CharlesPikachu/DecryptLogin/blob/bb4228c0535ffd7060b7816cbd1da51ba8d95ab8/DecryptLogin/modules/core/baidupan.py)：用于理解二维码、轮询和登录交换的接口关系，不作为当前接口必然兼容的证明。
- [Go 扫码流程参考实现](https://github.com/peterq/pan-light/blob/867eee7a9251a0a5e6ac5367b1ec84c98b6cfb1d/pc/login/baidu.go)：用于核对独立会话与 CookieJar 的使用方式。
