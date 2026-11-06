# MujicAgent

一个轻量的原生 Codex 桌面客户端，基于 [MyGo](https://mygo.egoist.dev/) 0.7.4 和 Go 构建。全部界面使用 MyGo Native UI：没有 WebView、HTML、CSS 或 JavaScript 前端。

## 已实现

- 原生毛玻璃侧栏、紧凑单行会话、统一浅色界面（不跟随系统深色）；悬停高亮，遵循系统“减少动态效果”设置。
- 项目以可折叠文件夹显示，默认折叠并在重启后记住展开状态。每个展开区最多显示十行会话，其余通过滚轮查看；列表只构建可见范围及少量预加载行。悬停项目行时，箭头旁显示新建会话的 `+`。
- 添加本地项目，也可直接开始无项目对话。
- 手动**收纳（Settle）**：悬停或右键收纳会话；底部“收纳”显示数量，点击向上展开，最多显示五行，其余通过滚轮查看。列表只构建可见范围及少量预加载行，完成回复不会自动收纳。
- 会话右键删除，必须经过二次确认。删除本应用的会话记录，保留项目文件和 Codex 自身的历史。
- 本地 Codex App Server 连接、模型与思考强度选择、流式 Markdown、代码块、执行过程、停止生成、重连后继续会话。
- 正式回复之间的思考、命令、文件更改等合并为默认折叠的过程框，按类型显示次数与 Lucide 图标；可逐层展开，失败操作有独立标记。
- MCP 调用显示工具名、服务名、参数摘要和执行状态；展开可查看参数、结果和错误，较长内容可滚动查看。缺失数据会明确提示，历史记录中未保存的详情无法补回。参数、结果和错误中的常见敏感字段、认证头及私钥在保存前脱敏；没有字段标识或认证格式的任意文本无法保证识别。
- **Ask**：选项或自由输入、多问题、私密输入。命令／文件审批支持允许本次、会话内允许、命令前缀规则、网络主机规则及拒绝，具体选项由 App Server 的审批请求决定；通过原始请求 ID 回传。
- 按对话选择只读、默认权限、自动审批（Approve for me）或完全访问，并展示服务端提供的自定义权限档位；遵循服务端管理策略，完全访问需要确认。
- 原生设置悬浮面板：保留背后的对话，右上角关闭或按 Esc 返回；Agents 当前只管理一个 Codex，支持自定义可执行文件、连接状态、账户状态和当前版本显示。
- 项目、会话、草稿和 Settle 状态保存在本机。
- 启动后自动检查 GitHub Release；设置中可下载签名更新、重启安装并查看历史更新日志。
- 简体中文／英文即时切换，默认中文；菜单、设置、Ask 操作及原生控件跟随语言，用户内容保持原文。

## 开发

需要 Go 1.27.3+，以及已安装并登录的 [Codex CLI](https://developers.openai.com/codex/cli)。

```sh
codex login
go tool mygo dev
```

应用默认寻找 PATH 中的 `codex`。macOS 下会读取用户登录 Shell 的 PATH，供 Codex 及其 Node 等运行时使用，因此从 Finder、Dock 启动也能找到终端中安装的 CLI；探测有超时限制，并保留原有 PATH 和常见安装目录。也可在 **设置 → 智能体（Agents）→ Codex 路径** 指定绝对路径，应用不会自动替换该选择。

```sh
go test -race ./...
go vet ./...
go tool mygo vet
go tool mygo i18n check
go tool mygo build
```

macOS 构建输出位于 `build/darwin-arm64/`，包含 `.app` 和 `.dmg`。本地和普通 CI 构建使用 ad-hoc 签名。Release 工作流使用 Developer ID 签名和 Apple 公证，更新包另外使用独立的 Ed25519 签名校验。已发布的 `v0.1.1` 使用 ad-hoc 签名，自动公证从后续发版开始启用。

可选的真实连接测试会使用当前 Codex 登录，发送简短的测试消息并产生模型用量；不要求读取或修改项目文件：

```sh
MUJICAGENT_LIVE_TEST=1 go test ./internal/controller -run 'TestLiveCodex|TestLiveAsk' -v -timeout 4m
```

## 使用

点击输入框右侧的 `+` 选择图片或文件，也可直接粘贴复制的文件或图片。附件以小缩略图／文件图标和文件名嵌入正文，不带底色或下划线；前后文字在同一输入框连续编辑，支持选择、复制、退格删除和撤销。发送后保持相同的行内样式。剪贴板图片保存到应用数据目录，普通文件保留在原位置。

图片使用 Codex 的 `localImage` 输入；普通文件仅传递本地绝对路径，由 Agent 按当前权限读取。支持预览 PNG、JPEG、GIF 首帧、WebP 和 BMP；无法解码或超过预览限制（32 MiB／2400 万像素）的文件作为普通文件插入。文件保留在原位置，移动或删除后可能无法访问。未知扩展名使用通用 Lucide 文件图标。

点击图片附件打开预览，在图中点击位置后会弹出思考气泡，在气泡中填写说明并点击「插入标记」。标记以从左上角计算的 `(x%, y%)` 和说明插入当前草稿，随图片发送给 Agent；草稿中的附件原位更新，已发送的消息保持原样。内部链接格式为 `<mujica://image/?path=...>` 或 `<mujica://file/?path=...>`，路径和标记参数使用 URL 编码，仅在应用内解析。

在 **设置 → 日志** 查看本次运行的日志，最新记录在前，最多保留 200 条，不写入磁盘。开发者模式默认关闭；打开后才显示暂不支持的 Codex 请求等诊断记录，开关会保存到本机。普通日志不再占用页面顶部。

找不到 Codex、连接失败等错误不受开发者模式限制：主界面右上方显示红色浮动通知，包含错误图标、标题、正文和关闭按钮。打开设置时可在日志页查看错误，关闭通知不删除日志。

点击 `+` 新建无项目会话，或用左下角文件夹按钮添加项目，再点击项目旁的 `+`。Enter 发送，Shift+Enter 换行，Cmd/Ctrl+N 新建会话。

对话进行中可继续发送，消息进入输入框上方的等待队列。拖动左侧手柄调整顺序；正常完成整轮后，只发送下一条。点击「插入对话」通过 `turn/steer` 引导当前轮，由 Codex 在工具调用边界处理；成功后从队列移除并显示在当前轮记录中，失败保留原消息。停止、失败、断线或重启后队列暂停，可点击「继续发送」。队列保存在本地；普通发送和 Steer 共用附件解析与当前会话上下文。

点击输入框底部的 `模型 · 思考强度 · 速度`（普通速度不额外标注），面板以 220 毫秒淡入并轻微上移，关闭时以 160 毫秒淡出并轻微下移。左侧选择或搜索模型，右侧通过原生分档滑块调整强度，刻度来自该模型支持的档位，支持拖动和方向键。兼容的强度会保留，否则切换到模型默认值；调整时保持面板打开，点击外部或按 Esc 关闭。启用系统“减少动态效果”时直接显示或关闭。

面板右下角的 Fast／Ultrafast 上下排列，每项占一整行，来自 App Server `model/list` 的 `serviceTiers`，只显示当前模型提供的速度选项。点击切换，再次点击取消，均未选中时使用普通速度；换模型会保留兼容的速度，否则恢复普通速度。选择保存在本机，并通过 `turn/start.serviceTier` 传递服务端返回的 ID；普通速度明确传递 `default`，避免继承之前的加速设置。

在**设置 → 外观 → 语言**选择简体中文或 English，立即生效并保存。空白会话在中文显示“新对话”，收纳入口显示“收纳”；切换英文后分别显示“New conversation”和“Settled”。模型名称、思考强度代码、用户标题、对话内容和外部错误详情保持原文。

输入框旁的权限选择器通过 `permissionProfile/list` 按当前项目读取档位，结合 `configRequirements/read` 的审批策略限制禁用不可用选项。新建对话默认使用工作区权限；选择单独保存在该对话中，不影响其他对话。权限通过 `thread/start`、`thread/resume` 和每次 `turn/start` 的 `permissions`、`approvalPolicy` 传递。已加载的会话调整权限时，通过 `thread/settings/update` 提交后才保存新选择；失败时保留原选择并显示错误。

运行期间也可以为下一轮调整权限，选择器显示“当前权限 → 下一轮权限”，面板说明生效边界。Codex CLI 0.167.0 的 `thread/settings/update` 仅更新后续轮次；`turn/settings/update` 不支持权限档位或审批策略，因此本轮尚未开始的操作也仍使用本轮原权限。正在执行的操作、待处理审批和子会话均不会因选择变化而自动获得授权。下一轮显式发送新权限，保留原会话上下文；无需新建会话或重启。轮次启动中暂不能修改，需稍后重试。完全访问仍需确认，不更改全局默认权限。

自动审批沿用工作区权限和 `on-request` 策略，将 `approvalsReviewer` 设为 `auto_review`，由 Codex 审核审批请求。只有服务端启用 `guardian_approval` 时才显示，并遵循项目配置及 `allowedApprovalsReviewers` 限制；切回默认权限后由用户审批，不修改全局 Codex 配置。

每次对话显式请求 `summary: auto`，展示 App Server 返回的思考摘要。模型不保证每次都返回摘要；展开空记录时显示“未返回思考摘要”，进行中的空记录显示“等待思考摘要…”。历史记录中没有返回的内容无法补回。

对话使用默认协作模式，不再提供“计划”开关。Ask 出现后选取答案或直接输入，再点击“继续”。审批卡片优先使用服务端 `availableDecisions`：会话内允许仅作用于服务端定义的匹配范围；记住命令规则会显示前缀，网络规则会显示主机。未提供决策列表时按相应协议生成选项，绝不自行将一次允许提升为会话或永久授权。

## 架构

保持简单的 MVC，只有 MyGo 和它自带的依赖，没有额外前端框架、数据库或服务：

```text
main.go                        应用和原生窗口入口
internal/model/state.go        项目、会话、配置与原子文件保存
internal/controller/           用户操作、会话生命周期、事件归并
internal/view/                 原生视图、输入状态、交互与确认框
internal/codex/                stdio JSON-RPC 传输与协议类型
locales/                       MyGo 中英文语言目录及生成的类型安全访问器
```

后台操作通过事件队列回到 UI 线程修改状态；视图不直接操作子进程。App Server 按 `initialize → initialized → thread/start 或 thread/resume → turn/start` 工作。界面处理消息增量、工具事件、结束状态和服务端请求。协议以 [Codex App Server 文档](https://developers.openai.com/codex/app-server) 和本机 CLI 生成的 JSON Schema 校验。

修改 `locales/zh-Hans.json`、`locales/en.json` 后运行 `go tool mygo generate`；开发和构建命令也会生成访问器。语言使用稳定标识 `zh-Hans`／`en` 保存，旧数据默认使用中文。

数据位于 `os.UserConfigDir()/MujicAgent`，macOS 为 `~/Library/Application Support/MujicAgent`。`state.json` 使用临时文件加原子替换保存；无项目会话有各自的 `chats/<id>/` 工作目录。`MUJICAGENT_DATA_DIR` 可指定隔离的数据目录。凭据由 Codex 管理，应用不复制凭据文件。

## 发布与更新

安装包在 [GitHub Releases](https://github.com/ji233-Sun/mujicagent/releases)：Apple Silicon 选择 `arm64.dmg`，Intel 选择 `amd64.dmg`。将应用拖入可写的 Applications 目录后运行；磁盘映像内、开发模式或不可写目录不支持自动安装更新。

应用启动 10 秒后检查正式 Release，随后每 24 小时检查一次；**设置 → 软件更新**也可手动检查。发现新版本后，左下角出现更新入口。点击“下载更新”，由 MyGo 下载对应架构的包并校验签名，再点击“重启并更新”；有对话运行时暂停安装和重启。更新不改动本地项目、会话和 Codex。更新日志使用原生 Markdown 展示最近 20 条正式 Release，旧版本可展开查看。

发布只通过 [Release 工作流](https://github.com/ji233-Sun/mujicagent/actions/workflows/release.yml) 手动触发：

1. 选择 **Run workflow → main**。
2. 选择 `patch`、`minor` 或 `major`，分别递增修订号、次版本或主版本；低位版本号自动清零。初始版本 `0.1.0`，第一次 patch 发布为 `0.1.1`。
3. 正式发版保留 `dry_run=false`。试运行勾选 `dry_run`：执行相同的校验、双架构构建、签名和 Apple 公证，只上传 Actions 构建产物，不修改版本、标签或 Release。试运行也允许选择开发分支，用于验证工作流改动。

工作流固定使用触发时的提交，根据 GitHub 自动生成的说明和上次发布以来的提交记录生成日志，更新 `mygo.json` 与 `CHANGELOG.md`。测试、公证和最终产物验证通过后，提交版本变更、创建 `vX.Y.Z` 标签；全部安装包与更新清单上传完成后才将草稿发布为正式 Release。普通 push 和 PR 仅运行 [CI](https://github.com/ji233-Sun/mujicagent/actions/workflows/ci.yml)，不会发版。发布前若 main 已前进，原子推送会拒绝覆盖，必须重新 **Run workflow**；旧运行的重试始终保留原提交。

发布分为 `build → submit → finalize → publish`，中间产物保留 30 天：

- `release-build` 保存固定版本的签名产物、源码提交、发布说明与 SHA256 清单。
- `release-submission-arm64/amd64` 分别保存 Apple submission ID、架构、版本和原始 DMG SHA256；提交完成即保存，不等待公证结果。
- `release-final-arm64/amd64` 保存装订后验证通过的 DMG、更新包、更新清单与 SHA256 清单。
- `notary-logs-*` 保存每次等待的状态和可获取的 Apple 公证日志。

网络失败或等待超时后，在**原运行**选择 **Re-run failed jobs**，或仅重跑对应任务。已保存的构建和 submission ID 会被复用，不会重新编译或重新提交公证。**不要选择 Re-run all jobs**：GitHub 会清除上一轮 artifacts；脚本发现重试缺少构建检查点会停止，避免悄悄重新构建。若首次构建失败、尚未保存检查点，或检查点已丢失，需要新建 **Run workflow** 明确重新构建。`finalize` 等待同一 ID 最多 20 分钟，只有 `Accepted` 才装订；`staple` 和 `validate` 的临时网络错误最多尝试 5 次，间隔 10、20、40、60 秒。`Invalid/Rejected` 必须检查 Apple 日志并修复代码，重复等待不能修复拒绝原因。

检查点过期或缺失时不能保证恢复。特别是 Apple 已接受上传、但客户端未收到 ID，或 ID 尚未成功上传为 Actions 产物时，需先检查 Apple 提交历史；重试可能再次提交。新建 **Run workflow** 是全新发布，不读取旧运行的检查点。

### Apple notarization

在[仓库 Actions Secrets](https://github.com/ji233-Sun/mujicagent/settings/secrets/actions) 中配置以下五项：

| Secret | 内容 |
| --- | --- |
| `MACOS_CERTIFICATE_P12` | 包含私钥的 **Developer ID Application** `.p12` 文件的 Base64 编码 |
| `MACOS_CERTIFICATE_PASSWORD` | `.p12` 导出密码 |
| `APPLE_API_KEY_ID` | App Store Connect 团队 API Key ID |
| `APPLE_API_ISSUER_ID` | 团队 API Issuer ID |
| `APPLE_API_PRIVATE_KEY` | `.p8` 文件的完整原文，保留 BEGIN/END 行与换行 |

证书必须是用于 Mac App Store 之外分发的 **Developer ID Application**，不能使用 Apple Development、Apple Distribution 或 Developer ID Installer。证书需要同时导出私钥，`.p12` 中只放一个有效的 Developer ID Application 身份。证书与 API 密钥应属于同一开发者团队。参见 [Apple 证书说明](https://developer.apple.com/help/account/certificates/create-developer-id-certificates/)和[团队 API 密钥说明](https://developer.apple.com/help/app-store-connect/get-started/app-store-connect-api/)。

可以在本地直接上传文件内容，避免把私钥打印到终端或写进仓库：

```sh
base64 -i "/path/to/DeveloperID.p12" | gh secret set MACOS_CERTIFICATE_P12 --repo ji233-Sun/mujicagent
gh secret set MACOS_CERTIFICATE_PASSWORD --repo ji233-Sun/mujicagent
gh secret set APPLE_API_KEY_ID --repo ji233-Sun/mujicagent
gh secret set APPLE_API_ISSUER_ID --repo ji233-Sun/mujicagent
gh secret set APPLE_API_PRIVATE_KEY --repo ji233-Sun/mujicagent < "/path/to/AuthKey.p8"
```

构建阶段创建临时 Keychain 导入证书，MyGo 对两个架构的应用和 DMG 签名，以 `-skip-notarize` 保存待公证产物。提交和等待阶段使用临时 API 私钥文件。公证 Accepted 后，将票据附加到 DMG 和用于自动更新的应用，再重新打包并生成对应最终归档的 Ed25519/SHA256 签名；保留 MyGo 更新清单的版本、URL、说明和日期，当前要求 `updates.deltas=0`。发布前会检查代码签名、DMG 的公证票据，并解压自动更新包验证其中应用的票据。任何一步失败都不发布；凭据缺失时也不会降级为 ad-hoc 发布。任务结束时清理临时 Keychain 和私钥文件。

签名配置只在 CI 构建期间注入，发布提交不包含证书身份和 runner 路径。首次配置完成后，建议运行一次 `dry_run=true`，确认签名、公证、票据与更新归档检查全部通过，再正式发布。

仓库 Actions Secret `MYGO_UPDATER_PRIVATE_KEY` 存储更新签名私钥的**内容**。公钥固定在 `mygo.json`；私钥不进仓库、不进入应用。维护者须安全备份私钥，丢失或随意替换会让旧客户端无法验证后续更新。只有签名构建会生成 `update-darwin-*.json` 和对应 `.tar.gz`；两者必须与 `.dmg` 一起保留在 Release 中。此签名不能替代 Apple Developer ID 公证。

若上传失败留下草稿，重跑原运行的失败任务即可复用 `release-final-*` 补齐资产。发布脚本只接受与原提交和版本文件完全匹配的已有标签，仅补传草稿；若版本已正式发布则直接结束，不覆盖已发布资产。

## 范围与限制

当前验证环境是 macOS arm64、MyGo 0.7.4、Codex CLI 0.167.0。Windows、Linux 尚未做运行验证；毛玻璃使用系统提供的材质，不支持材质的平台使用实色侧栏。

此版本不包含 MCP 管理、remote、worktree、多个 Codex 实例或其他 Agent 接入。现有 Codex 用户配置仍由 Codex 自身加载。Ask 等协议仍在演进，需要支持对应 App Server API 的 CLI。

应用非正常关闭时，未完成回复可能只保留最近保存的本地快照，重启后标为中断；已保存会话可重连继续。尚未实现从 Codex 的全局历史导入或同步其他客户端的修改。

## 致谢

- [pingdotgg/t3code](https://github.com/pingdotgg/t3code)：参考了 Codex App Server 连接、Ask 应答、项目／会话组织、手动 Settle 与 Agent 管理的交互思路。MujicAgent 以 Go Native UI 重新实现这些最小功能；相关 MIT 许可见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
- [egoist/mygo](https://github.com/egoist/mygo)：提供原生 GPU UI、Markdown、系统毛玻璃、桌面 API、无窗口测试和打包工具。
- [Lucide](https://lucide.dev/)：界面通用图标使用官方 SVG，由 MyGo 原生绘制；来源与许可见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
- [openai/codex](https://github.com/openai/codex)：提供本地 Codex App Server。

视觉布局根据用户提供的 Rex 界面参考重新设计，没有嵌入 t3code 的 Web UI。
