# MujicAgent

一个轻量的原生 Codex 桌面客户端，基于 [MyGo](https://mygo.egoist.dev/) 0.7.4 和 Go 构建。全部界面使用 MyGo Native UI：没有 WebView、HTML、CSS 或 JavaScript 前端。

## 已实现

- 原生毛玻璃侧栏、紧凑单行会话、浅色／深色主题；新会话滑入，悬停高亮，遵循系统“减少动态效果”设置。
- 添加本地项目，也可直接开始无项目对话。
- 手动**收纳（Settle）**：悬停或右键收纳会话；底部“收纳”显示数量，点击向上展开，最多显示五行，其余通过滚轮查看。列表只构建可见范围及少量预加载行，完成回复不会自动收纳。
- 会话右键删除，必须经过二次确认。删除本应用的会话记录，保留项目文件和 Codex 自身的历史。
- 本地 Codex App Server 连接、模型与思考强度选择、流式 Markdown、代码块、执行过程、停止生成、重连后继续会话。
- 正式回复之间的思考、命令、文件更改等合并为默认折叠的过程框，按类型显示次数与 Lucide 图标；可逐层展开，失败操作有独立标记。
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

应用默认寻找 PATH 中的 `codex`。如果从 Finder 启动时找不到，可在 **设置 → 智能体（Agents）→ Codex 路径** 填写绝对路径。

```sh
go test -race ./...
go vet ./...
go tool mygo vet
go tool mygo i18n check
go tool mygo build
```

macOS 构建输出位于 `build/darwin-arm64/`，包含 `.app` 和 `.dmg`。构建目前使用 ad-hoc 签名，尚未配置 Apple Developer ID 签名和公证；首次打开可能需要在系统设置中允许。更新包另有独立的 Ed25519 签名校验。

可选的真实连接测试会使用当前 Codex 登录，发送简短的测试消息并产生模型用量；不要求读取或修改项目文件：

```sh
MUJICAGENT_LIVE_TEST=1 go test ./internal/controller -run 'TestLiveCodex|TestLiveAsk' -v -timeout 4m
```

## 使用

点击 `+` 新建无项目会话，或用左下角文件夹按钮添加项目，再点击项目旁的 `+`。Enter 发送，Shift+Enter 换行，Cmd/Ctrl+N 新建会话。

点击输入框底部的 `模型 · 思考强度 · 速度`（普通速度不额外标注），面板以 220 毫秒淡入并轻微上移，关闭时以 160 毫秒淡出并轻微下移。左侧选择或搜索模型，右侧通过原生分档滑块调整强度，刻度来自该模型支持的档位，支持拖动和方向键。兼容的强度会保留，否则切换到模型默认值；调整时保持面板打开，点击外部或按 Esc 关闭。启用系统“减少动态效果”时直接显示或关闭。

面板右下角的 Fast／Ultrafast 上下排列，每项占一整行，来自 App Server `model/list` 的 `serviceTiers`，只显示当前模型提供的速度选项。点击切换，再次点击取消，均未选中时使用普通速度；换模型会保留兼容的速度，否则恢复普通速度。选择保存在本机，并通过 `turn/start.serviceTier` 传递服务端返回的 ID；普通速度明确传递 `default`，避免继承之前的加速设置。

在**设置 → 外观 → 语言**选择简体中文或 English，立即生效并保存。空白会话在中文显示“新对话”，收纳入口显示“收纳”；切换英文后分别显示“New conversation”和“Settled”。模型名称、思考强度代码、用户标题、对话内容和外部错误详情保持原文。

输入框旁的权限选择器通过 `permissionProfile/list` 按当前项目读取档位，结合 `configRequirements/read` 的审批策略限制禁用不可用选项。新建对话默认使用工作区权限；选择单独保存在该对话中，不影响其他对话。权限通过 `thread/start`、`thread/resume` 和每次 `turn/start` 的 `permissions`、`approvalPolicy` 传递，运行中的对话暂时不能切换。

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
3. 正式发版保留 `dry_run=false`。试运行勾选 `dry_run`：执行相同的校验、双架构构建和签名，只上传 Actions 构建产物，不修改版本、标签或 Release。

工作流根据 GitHub 自动生成的说明和上次发布以来的提交记录生成日志，更新 `mygo.json` 与 `CHANGELOG.md`。测试通过后构建 macOS arm64/amd64，提交版本变更、创建 `vX.Y.Z` 标签；全部安装包与更新清单上传完成后才将草稿发布为正式 Release。普通 push 和 PR 仅运行 [CI](https://github.com/ji233-Sun/mujicagent/actions/workflows/ci.yml)，不会发版。发布期间若 main 已前进，原子推送会拒绝覆盖，重新运行即可。

仓库 Actions Secret `MYGO_UPDATER_PRIVATE_KEY` 存储更新签名私钥的**内容**。公钥固定在 `mygo.json`；私钥不进仓库、不进入应用。维护者须安全备份私钥，丢失或随意替换会让旧客户端无法验证后续更新。只有签名构建会生成 `update-darwin-*.json` 和对应 `.tar.gz`；两者必须与 `.dmg` 一起保留在 Release 中。此签名不能替代 Apple Developer ID 公证。

若上传失败留下草稿，可在该次 Actions 的 `release-X.Y.Z` 产物中找到安装包和清单，补齐同一版本的草稿再发布；不要删除已分发的更新资产或覆盖已发布版本。

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
