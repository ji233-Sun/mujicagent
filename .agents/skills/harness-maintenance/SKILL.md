---
name: harness-maintenance
description: 为 MujicAgent 添加或维护 harness、模型发现与收藏、会话交接、智能体设置、安装更新和认证时使用，包含紧凑 UI 及 Windows 兼容约束。
---

# 接入与维护 Harness

本技能用于本仓库的 Harness 集成。交付遵循 [mujicagent-delivery](../mujicagent-delivery/SKILL.md)，UI 生命周期及 CPU 截图遵循 [mygo-maintenance](../mygo-maintenance/SKILL.md)。

## 从现有实现接入

当前支持 Codex、Pi、Droid。先按本次范围读取实现；路径相对仓库根目录：

| 范围 | 入口 |
| --- | --- |
| Harness 目录、CLI 协议与模型目录 | `internal/harness/codex.go`、`internal/harness/agents.go`、`internal/harness/models.go` |
| 任务生命周期、交接、模型缓存 | `internal/controller/agents.go`、`internal/controller/controller.go`、`internal/controller/queue.go` |
| 独立安装状态、探测及更新 | `internal/controller/harness.go` |
| 桌面 PATH 与 Windows 启动器 | `internal/codex/executable.go` |
| 会话归属、模型、收藏持久化 | `internal/model/state.go` |
| 共享选择器、官方图标、紧凑设置 | `internal/view/model_picker.go`、`internal/view/agent_picker.go`、`internal/view/agents.go` |

新增 Harness 使用稳定 driver ID，串起目录、路径解析、`Thread.Driver()`、`HarnessFor(id)`、协议和 UI；检查现有白名单及分派点，不能只增加侧栏图标。`Drivers` 是展示目录，不是自动协议注册机制。安装更新与会话协议分开；状态、路径、版本、账户及 session ID 按 Harness 隔离。保留旧记录默认为 Codex 的行为，不为单个新接入重写框架。

先确认官方 CLI/SDK 的当前协议：模型目录、会话创建与续聊、模型/思考强度参数、停止、输入附件、工具权限、流式或最终结果。记录真实支持范围，只有 CLI 版本探测不能证明认证或推理成功。能力缺失时不假装与 Codex 等价；未实现的附件类型需明确拒绝，权限不能静默放大。

## 新增 Harness 的权限与附件验收

- 权限入口复用 Codex 的 `pickerOption`、`pickerPopover`、权限图标、选中标记和高权限确认，不向用户展示工具开关或无解释的 low/medium/high。名称按真实能力映射，不能把不同 Harness 的执行权限说成同一种 OS 沙箱。
- Pi 当前「只读」只启用内置 read/grep/find/ls，并关闭扩展和 MCP；「完全访问」沿用本机工具配置。没有原生沙箱时不添加假工作区权限。参考 Pi 官方 permission-gate 扩展和其他客户端实现时，先验证当前安装版本的实际能力；不要仅注入 prompt 声称权限已受限。
- Droid 使用 `exec --input-format stream-jsonrpc --output-format stream-jsonrpc`。initialize/load 后，每轮显式 update_session_settings：interactionMode=auto、autonomyLevel=off/low/medium/high，以及所选模型与思考强度；权限请求必须经用户选择，仅回传 proceed_once/cancel，AskUser 复用共享请求界面。消息、工具、压缩、失败和完成通过 session_notification 处理；恢复历史不得追加到当前轮。
- Pi 使用 `--mode rpc`，通过 prompt/steer 与 extension_ui_request/response 双向交互，等待 agent_settled，不能将 agent_end 当作整个任务结束。
- 新 Harness 在 `internal/harness/agents.go` 注册运行与 Steer 适配器，使用 AgentEvent/ToolEvent/Interaction 契约；缺少 Steer 实现时不启用入口。复用 `mergeAgentEvent`、model.Item、现有请求 UI 与脱敏详情，不写只消费最终文本的旁路。必须扩展现有协议、状态和视图测试，覆盖事件边界、同名并行工具、增量与快照、审批回传、停止、持久化和历史过滤。登记能力未验证项，不以编译通过宣称完整对齐。
- 交互复用 `ask` 的 Interaction/Resolved 生命周期，回答、取消和超时都清理请求；UI 请求使用独立 ID，不能用可重复的上游 ID 作为跨会话答案缓存键。普通选择/输入用 `client/form` 复用现有表单校验，MCP elicitation 保留原方法，不能把普通表单标为 MCP。Droid picker 保留原选项 ID，支持单/多选、预选、搜索、刷新、空列表和取消；AskUser 多选回传完整答案。Pi editor 保留预填、多行和请求超时；审批选项必须来自实际协议，未提供的一次允许不能补造。
- MCP 元数据只消费有来源的字段：Pi 工具结果/进度的 `details.server/tool`、Droid 审批的 `mcp_tool` 详情；后续稀疏事件保留已识别服务和类型，不由工具名称猜服务名。Droid TodoWrite 的 `todos` 是字符串；Pi 官方 plan-mode 扩展的 `plan-todos` 组件可转换为计划文本，清除组件不抹去历史。计划展示/交接、计划执行模式和通用 MCP elicitation 分别验收，不能互相替代；Pi 只读模式关闭扩展时不会产生该组件。
- Pi/Droid 图片通过各自 RPC 的 MIME + base64 image payload 发送，普通文件复用已有本地路径输入，标注发送原图与导出图。先实测原生能力；确实不支持时才考虑托管文件及 URI 映射，并定义模型可访问的解析规则、生命周期和错误反馈，不能只生成模型无法读取的自定义 URL。
- 事件负载按事件类型解析：工具结果可能为对象或数组，不得强制解码为文本。恢复会话的历史通知不能重复追加到当前回复；停止、错误、缺失能力和未知客户端请求必须有明确结果，子进程必须回收。
- 在既有测试补权限默认值、未知值拒绝、图片内容、通知类型、恢复和取消回归。获得实际 token 授权后，用用户指定的精确模型验证读取普通文件、图片识别和第二轮续聊；只读目录探测不能替代这些证据。`MUJICAGENT_AGENT_LIVE_TEST=1 go test ./internal/harness -run TestInstalledAgentTurns -v -count=1` 会调用 Pi 的 magpie/workbuddy/deepseek-v4.1-flash 和 Droid 的 custom:magpie/codex-fast/gpt-6.1-sol，产生用量。

## 新增 Harness 的 Issue 标签

- 每次新增 Harness，必须在同次交付中为 GitHub 仓库创建 `harness:<driver-id>` 标签，后缀使用新 Harness 的稳定 driver ID；说明写明对应 Harness，颜色沿用已有 Harness 标签。先查询已有标签，存在则复用，不按显示名另建同义标签。
- 同步更新本技能的支持列表、[Issue 提报规范中的映射](../mujicagent-delivery/SKILL.md#issue-提报与关联) 及 [README 提报说明](../../../README.md#参与项目)，让提报者能够选择新 Harness。标签选择范围及无权限、待确认情况统一遵循提报规范。
- 交付前回读远端标签，核对名称、说明与 `Drivers` ID 一致，并在 PR 中记录结果。仅修改本地文档不算完成标签维护；创建标签遇到权限或认证阻塞时，按交付技能报告未完成项。

## 模型选择与紧凑界面

- 模型选择以 Codex 为参照，复用 `modelCatalogPanel` / `modelChoicesPanel` 和 `modelEffortSlider`。在对话输入栏选择具体模型，不新增「默认模型」、手填模型 ID 或设置页 Models 控件。设置页只放版本、安装/登录入口及折叠连接详情，不铺满协议解释文字；异常通过简短状态和可重试入口表达。
- 模型目录从 Harness 的官方接口自动读取，包含其已配置的自定义模型；不要硬编码模型名单。已有实现：Codex `model/list`、Pi `get_available_models`、Droid `droid.list_models`。名称用于展示，精确 ID 用于选择与发送；思考强度及速度依据真实能力，不能把 Codex 档位强塞给其他 Harness。
- 发现目录是只读动作，不创建会话、不发送推理。只从打开选择器、切换 Harness 或刷新等动作发起，禁止在渲染中启动进程。复用异步队列、超时和进程树取消；缓存区分可执行文件与工作目录，窗口关闭或替换请求时取消，回调拒绝过期结果。失败显示空列表或错误，不伪造一个可用模型；其他 Harness 的目录缺失不能锁死整个入口。
- 目录只保留展示及选择字段，不把 provider 端点、密钥或原始诊断写进应用状态。进程无论成功、失败还是取消都要回收。
- 收藏键始终是严格 `(Harness, Model ID)` 二元组，持久化和列表身份复用 `model.ModelFavorite`；不能仅按模型名或 ID 合并。所属 Harness 内稳定置顶，点击星标不选中模型；顶部星标汇总全部收藏，即使目录暂不可用也保留已收藏 ID。
- 左侧使用紧凑的 Harness 图标栏，名称以 Tooltip 展示。普通模型行不显示 Harness 图标，全部收藏页才在行首显示。设置二级侧栏统一左对齐、图标尺寸及间距。
- 图标取自官方品牌资源、官网或官方仓库，原文件存入 `internal/view/assets`，在 `THIRD_PARTY_NOTICES.md` 记录来源；不要用通用星光、机器人或自行绘制的近似图标代替。彩色官方图标使用保留颜色的渲染方式。

## 会话执行与交接

- 同 Harness 换模型保留会话，下一轮使用所选 ID；调整思考强度和速度时保留兼容值，否则使用该模型支持的值。跨 Harness 复用 `Handoff`，不要把一个 CLI 的 session ID 交给另一个。
- 有历史的交接建立关联新会话，带入可见对话及项目目录，下一次用户发送时才交付上下文。原记录、草稿、队列保留，队列不自动复制执行；新会话不继承工具权限。运行中或编辑队列时不交接。历史过大明确报错，不静默截断；隐藏推理及 CLI 私有状态不迁移。
- 复用任务队列、附件转换、停止和进程树清理。流式增量与最终完整回复分清，失败/停止不自动继续队列；保存归属、模型及续聊标识，测试重启和再次发送。不可恢复的状态先说明实际限制，不增加会伪装成续聊的重放路径。

## 安装、认证与跨平台

- 保留已有 CodexPath 与会话格式；新增设置使用向后兼容的默认值。Codex 自动检查默认每天，只查版本，不安装；Pi/Droid 当前由官方 CLI 管理安装与登录。手动操作显示进度、错误、取消及可重试入口；所有状态通过 Controller 队列发布，禁止在渲染中启动网络或进程。
- 探测必须沿用连接使用的桌面 PATH 解析，并尊重显式路径。只有证据明确时才能用 npm/Homebrew 原地更新；未知安装只能明确提供“安装并切换到托管版”，不覆盖原文件。任务运行或连接中禁止更新。
- Codex 官方渠道采用完整原生发行包，不能只复制 codex 可执行文件：伴随 host、rg 等也需要保留。大陆选择 npm 镜像，在私有 prefix 安装；无 npm 或 Windows 使用镜像平台包，提供免 Node 选择。不要全局安装 Node、修改 PATH/npm 配置或使用 sudo。
- 托管安装先验证 HTTPS 下载摘要、解包路径、版本及相应协议可用性（Codex 为 app-server），再切换配置路径。使用新目录，失败只清理本次临时目录；旧目录保留供原有进程使用。不要在更新过程中清理用户自定义目录。
- Windows 单独检查 amd64/arm64、空格/中文路径、PATHEXT、npm .cmd 与 .exe。Codex 已知 npm 布局直接解析原生 exe；Pi 已知 npm 启动器解析为 Node + 包内脚本；Droid 使用原生 exe。npm 更新经 node + npm-cli.js 参数数组执行。不要把用户路径插入 cmd.exe/PowerShell 命令字符串；未知 wrapper 要求显式 exe。更新前释放客户端句柄，新安装使用独立版本目录，避免覆盖运行中的文件。
- 认证由 harness 管理。Codex 的 account:null 不等于未登录：检查 requiresOpenaiAuth；API key、第三方 base_url 与 env_key 配置由原有 Codex 配置和进程环境继承，不复制密钥到应用状态、日志或 UI。配置状态不代表凭据已验证。
- account/read 仅对 -32603 且 workspace routing discovery timed out 做最多三次、500ms/1s 退避；独立请求预算、可取消。不重试认证错误，不重放 turn/start、登录或其他写请求。

## 按变更范围验证

扩展现有 `internal/harness/codex_test.go`、`internal/controller/controller_test.go`、`internal/view/agent_picker_test.go`：选择与本次行为相关的协议参数、目录解析、过期结果/取消、session 续聊/交接、收藏隔离和 UI 状态，不另建测试框架。安装变更另测缺失/外部/托管来源、失败不切换、摘要/路径校验、版本比较与认证状态。

布局直接设置场景状态并生成中英文 CPU 离屏 PNG，检查正常/加载/失败模型目录、无默认或手填入口、无设置页 Models、收藏图标与侧栏对齐。截图场景不通过模拟点击切换；单独的交互回归可验证收藏不触发选中。按交付技能选择相关 Go 测试、并发 race、MyGo vet、生成与 i18n 检查及 Windows 构建。

`MUJICAGENT_MODEL_DISCOVERY_TEST=1` 是本机 CLI 只读目录探测；`MUJICAGENT_INSTALL_TEST=official` 或 `cn` 是写入临时目录的原生包下载测试，按需显式开启。它们不是实际推理验证；真实发送会产生用量，遵循交付技能的授权边界。CPU 截图和交叉编译不能替代 GPU/原生窗口或 Windows 原生执行证据。

协议与标识参考：[Pi RPC](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/rpc-commands.md)、[Pi 品牌资源](https://pi.dev/press-kit)、[Droid SDK](https://docs.factory.com/sdk/typescript)、[Factory 官网](https://factory.com/)。新增接入时查对应 Harness 的官方来源，现有例子不是新 CLI 的协议保证。
