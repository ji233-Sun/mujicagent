# Droid 工具与事件适配

本次核对对象为本机 `/Applications/Factory.app/Contents/Resources/bin/droid`
（`droid --version`：1.6.0）及同一 Factory.app 的 `app.asar`。
通过只读检查 CLI 内嵌协议 schema、JSON-RPC adapter 和桌面端会话解析确认以下行为。
没有读取认证材料，没有修改安装文件，也没有发送真实模型请求。

## Script 批量调用

- 外层 Script 通过 `tool_call` 发布，代码来自 `toolUse.input.script`。
- 实际执行的子工具通过 `create_message.message.content` 中的 `tool_use`
  发布，包含 `id/name/input/scriptExecution`。`scriptExecution.outerToolUseId`
  是父调用 ID，`runId/seen` 是运行及批次元数据。不能通过分析 JavaScript
  猜测哪些调用已经执行。
- 子调用结果通过 `tool_result` 发布，按 `toolUseId` 更新；消息快照中的
  `tool_result` 同样处理。数组及对象结果保留原始结构。
- `tool_progress_update` 携带 `text/fullOutput/error/parameters`。
  文本增量追加，完整输出替换；`tool_execution_phase_changed` 和
  `tool_execution_heartbeat` 可在完整参数之前提供名称。后到的消息元数据
  补齐名称、参数和父调用，不把已完成的调用重新置为运行中。
- `create_message` 只提取工具块，避免重复文字和思考增量。
  `llm_only`、`hiddenFromUserViews` 和初始化／恢复阶段的通知不追加到当前轮。
- 相同名称的并行调用使用各自 ID；后台 Script 的等待调用保留独立身份。
  父调用关联、参数、结果和终态沿用共享模型保存及恢复。

## 工具目录与展示

工具集合会随模型、组织策略、插件、连接器、MCP 和延迟加载变化，不能把安装包
里的名称表当作每个会话都可调用的白名单。以下是本机版本内嵌名称表与当前运行环境
可核对的目录；名称及参数始终来自真实事件，未知名称也保留详情。

| 工具 | 展示与处理 |
| --- | --- |
| Execute、Script、WaitForScript | 共享命令流程及输出查看器；Script 展示实际代码 |
| Edit、MultiEdit、Create、ApplyPatch | 共享文件变更流程；路径、编辑／写入内容及 patch |
| Read、LS | 共享紧凑工具流程及文本输出 |
| Grep、Glob、WebSearch、FetchUrl | 共享搜索／网页流程及结果 |
| TodoWrite | 共享计划组件，解析字符串计划和状态 |
| AskUser | `droid.ask_user` 往返共享单选、多选、自定义回答及取消 |
| Compact／原生压缩事件 | 共享上下文压缩流程，见下节 |
| Skill、ToolSearch、ConnectApp、ConnectorSearch、GenerateDroid、GenerateImage、DecisionModel、getIdeDiagnostics | 保留真实名称，使用共享工具行和参数／结果查看器 |
| Task、TaskOutput、TaskStop、SpawnAgent、ListAgents、SendMessage、InterruptAgent、RenameAgent、ArchiveAgent、CreateListener | 保留各调用身份及工具详情；不把子会话内容伪造成当前会话事件 |
| ExitSpecMode、RecommendMission、ProposeMission、StartMissionRun、SelectFeature、EndFeatureRun、DismissHandoffItems、CreateDataset | 保留真实调用及参数／结果；不等同于已实现独立 Mission 工作区 |
| StageSettingsChanges、Loop、ListAutomationTemplates、CreateAutomation、ListAutomations、ReadAutomation、EditAutomation、DeleteAutomation、RunAutomation | 共享工具详情，不在客户端重执行这些操作 |
| MCP／连接器／未来新增工具 | 名称、脱敏参数、结果及错误保留；只有真实 MCP 元数据才能设置服务身份 |

名称表包含有条件启用的工具，并不证明对应服务已授权或执行成功。
通用详情适配也不代表客户端实现了 Factory 自身的所有专用产品界面。

## 等待用户回答

- `droid.ask_user`、权限和选项请求交给独立处理任务，stdout 继续读取通知、
  其他请求和消息确认；不会等待第一份答案后才展示下一份问卷。
- `ask_user_resolved.requestIds` 和 `permission_resolved.requestId` 撤销对应等待；
  停止、会话结束或管道关闭会清理全部等待任务。已撤销请求不回传迟到答案。
- 问卷保留 `index/question/options/optionDescriptions/multiSelect`；回答携带原始
  问题和索引，多选与自定义回答完整回传。空问卷返回协议错误，不打开无法回答的表单。
- Pi 同样异步等待扩展表单，保留 editor 预填、多行和超时；Claude Code 使用原有
  异步 control 请求，Codex 使用持久连接上的 server request。
- 四个 Harness 共用限高滚动请求区，多个请求和待发消息不会挤走输入框及停止按钮。
  新请求不会继承已结束请求的滚动位置和答案。

验证使用本地协议子进程，通过 Controller 的发送、唤醒、回答、续聊及停止入口。
旧代码在两个未回答请求场景中，Droid/Pi 均只能发布第一个；修复后四个 Harness
均通过。中英文最小窗口截图使用 CPU 离屏渲染。此验证不代表已复现用户原会话的
全部事件顺序，也不代表真实模型、GPU 或系统原生窗口验收。

## Compact 生命周期

- 自动压缩开始：`droid_working_state_changed.newState=compacting_conversation`。
  重复开始通知不创建重复记录。
- 完成：`session_compacted`（`summaryId/removedCount/visibleBoundaryMessageId`）。
  相同 `summaryId` 去重，不删除本地可见历史。无开始通知的完成也保留独立记录，
  覆盖手动 `droid.compact_session` 的完成通知。
- CLI 成功但未保存新摘要时也会返回 `streaming_assistant_message`，
  此转换结束仍在进行的压缩。仅收到 `idle` 不视为成功证据。
- 会话错误或失败终态结束当前压缩为失败；取消结束为停止。
  子进程退出／连接中断时结束尚未完成的压缩，取消上下文显示停止，其余显示失败。
- 一轮多次压缩使用不同 ID；后续失败／停止不能覆盖历史完成记录。

当前没有新增手动压缩按钮或发送 `/compact` 的旁路。适配的是已有真实事件及
协议完成通知，不会凭名称伪造压缩成功。协议回归与 CPU 离屏截图不代表真实模型
压缩、GPU 或系统原生窗口行为已验证。
