# Codex BrowserUse 协议与装配边界

本模块提供 #30 的注册与分派接口，依赖 #26 的 [browser 契约](../internal/browser/README.md)。生产装配位于 `main.go`、`internal/controller/browser_runtime.go`、`internal/harness/codex_session.go` 及 `internal/view/browser_runtime.go`，共用 #29 的真实执行器。使用方法、平台限制和验收证据见 [运行说明](browseruse-runtime.md)。

## 协议依据

2026-11-09 使用本机 **codex-cli 0.163.0** 生成 schema，未初始化真实模型会话：

```sh
codex --version
codex app-server generate-json-schema --experimental --out /tmp/codex-schema
codex app-server generate-json-schema --out /tmp/codex-stable-schema
```

| 项目 | 本机 schema 结果 |
| --- | --- |
| `initialize.capabilities.experimentalApi` | 需显式声明；现有 `codex.Start` 已发送 `true`，假服务验证握手 |
| `v2/ThreadStartParams.dynamicTools` | 实验字段；不加 `--experimental` 的 schema 中没有该字段 |
| `DynamicToolSpec` | 本模块使用扁平工具：`type: "function"`、`name`、`description`、`inputSchema`；不使用命名空间、延迟加载 |
| `item/tool/call` | 服务端请求，参数包含 `threadId`、`turnId`、`callId`、`tool`、`arguments`；`namespace` 可省略或为 null |
| JSON-RPC ID | string 或 int64；按原始 `json.RawMessage` 回复，不转换为浮点数或使用 `callId` 替代 |
| `DynamicToolCallResponse` | `success` 与 `contentItems`；文本为 `inputText.text`，图片为 `inputImage.imageUrl` |
| 截图 | 文本元数据加 `data:image/png;base64,...` 图片项；不使用 Responses API 的蛇形字段，不创建文件 |
| `v2/ThreadResumeParams` | **没有 `dynamicTools`**；已有运行线程可按原 ID rejoin，不能借此补注册旧会话工具 |

[测试夹具](../internal/codex/testdata/browser-protocol-0.163.0.json) 由上述实验 schema 摘取，只保留检查所需的属性、required 和定义，不是完整的可独立解析 schema。协议测试使用该夹具和既有假 App Server；运行时检查使用所连接可执行文件及相同环境真正生成的完整 schema。

官方 [App Server 文档](https://developers.openai.com/codex/app-server) 在本次环境抓取返回 HTTP 403，因此字段结论以本机生成结果为依据。未运行真实模型调用，也未验证浏览器执行器和原生页面。

## 新建与恢复

每个 `codex.Client` 连接配一个 `codex.BrowserThreads`。第一次注册前，使用同一可执行文件、相同环境执行本地 schema 生成，最多等待 10 秒，并检查函数声明、调用、文本/图片响应、实验能力和恢复参数。缺少 schema 命令、字段或图片能力时，返回 `ErrBrowserRegistration`，不发送 `thread/start`；服务端拒绝注册也返回安全错误，不带服务端可能回显的输入，不重试或降级成 shell/MCP。

- `Start(ctx, params)` 复制参数并加入六个动态工具，记录成功返回的服务端 ID。
- `Resume(ctx, serverID, params)` 只允许本注册器在同一连接启动且仍未卸载的线程，不向 resume 添加 `dynamicTools`，并要求返回原 ID。
- 旧会话、重连后会话、从磁盘恢复且无法确认工具的会话返回 `ErrBrowserResume`。宿主应展示明确限制并保留原历史；可继续使用原有不带 BrowserUse 的续聊路径，但必须明确浏览器工具不可用。
- 显式卸载、unsubscribe 或归档前调用 `Forget(serverID)`。连接替换时丢弃整个注册器。宿主不能在外部卸载后继续把该线程视为运行中。
- 调用方不能通过 `threadId`、`history`、`path` 或自定义 `dynamicTools` 改写身份和注册行为。协议检查失败会留在该注册器内；用户重试检查时创建新注册器，不自动重建对话。

## 工具参数与结果

全部工具要求宿主提供的 `pageId` 和从 1 开始的 `navigation`：

| 工具 | 额外必填参数 |
| --- | --- |
| `browser_open` | `url`：不含 userinfo 的绝对 HTTP(S) 地址 |
| `browser_observe` | 无 |
| `browser_click` | `observationId`、`elementId` |
| `browser_fill` | `observationId`、`elementId`、`text`，允许空字符串替换全文 |
| `browser_scroll` | `deltaX`、`deltaY`，有限 CSS 像素数，至少一个非零 |
| `browser_capture` | 无 |

严格拒绝未知、重复、错误大小写、缺失、null 或类型错误的参数。模型不能传本地 ThreadID、Actor、权限、JS、selector、XPath 或原生句柄；没有创建页面、恢复控制或系统浏览器工具。宿主在工具上下文中提供当前页面标识，不能让模型猜测当前选中的页面。

请求在当前轮次上下文内执行，并受 `browser.DefaultTimeout`（15 秒）限制。`Sessions.Status` 只查询绑定的本地对话，核对页面、代次、状态与独立权限，然后固定 `ActorAgent` 调用六种契约。执行器仍须在实际投递前复核并串行化页面操作，验证并消费元素引用，完成脱敏；分派器不把 Status 快照视为最终授权。

结果采用专用 DTO，不序列化整个 Go 契约。动作只有已确认变化、有效页面及非空证据才成功；保留失败中的 `not_dispatched`、`dispatched` 或 `unknown`，不自动重试。Observe/Capture 错误不返回部分数据。观察文本和元素数有上限，输出 URL 再去除 userinfo、query、fragment；输入密码、Cookie、令牌和无法可靠脱敏的页面仍必须由执行器返回 `ErrSensitive`。

Capture 校验有效 PNG、声明尺寸和内存上限（4 MiB、单边最多 8192、最多 16 Mi 像素），只在当前请求内编码。图片能力缺失、尺寸超限或无效图片都明确失败，不产生成功占位。

## #31 的接线顺序

1. 每次连接创建注册器和 `controller.BrowserToolDispatcher`，回复函数绑定该连接的 `client.Reply`。
2. 新建会话使用注册器 `Start`；符合条件的同连接 rejoin 使用 `Resume`。保留既有 harness 的轮次启动和推理循环，只替换注册步骤。
3. 从可信 `model.Thread.ID`、`ServerID`、`turn/started` 通知或 `turn/start` 回执中的 turn ID，以及浏览器 `PageKey` 调用 `BindTurn`。开始通知和回执可能交错，不能只等回执而漏掉提前到达的调用。使用轮次上下文，不能绑定“当前选中对话”。同一轮次只绑定一次，重复绑定会拒绝；绑定下一轮取消上一轮。
4. `item/tool/call` 优先交给 `Handle`；其返回 false 时才走既有事件分派。绑定前的未知会话请求明确失败，不排队等待或猜测归属。
5. 将 `BrowserToolActivity` 投递至控制器事件队列，再按其本地 ThreadID 和 TurnID 校验并调用现有 `applyTurnEvent` / `mergeAgentEvent`。活动只含工具名、结果分类、投递状态和计数；不含填充值、URL、页面正文、证据、元素名称或图片。原始浏览器 `dynamicToolCall` 开始/完成快照由 harness 过滤，不能覆盖分派器的安全记录。
6. 停止调用 `StopTurn`；接管、切换对话、关闭页面调用 `ClosePage`，并通过 `Sessions.TakeOver/Close` 真正撤权和排空引擎。面板收起只改变可见性。陈旧的 turn ID / PageKey 不取消替换页面。
7. 连接尚可用时，宿主关闭先停止接收新调用并调用分派器 `Close`；这是异步取消，收到已接受调用的终态活动后再关闭传输（活动在回复尝试之后发布）。若已经断线则调用 `Disconnect`，取消工作、结束活动且不再安排回写。新连接不复用旧分派器。

JSON-RPC ID 在连接生命周期内保留去重标记；相同 ID（含等价转义）不再次投递或回复。不同请求 ID 重放同一 `(serverThreadID, turnID, callId)` 返回 stale，不覆盖原活动。标记只保留 ID，不缓存输入、页面内容或截图。回复写入失败也不重试。

取消可以先结束协议等待，再等引擎排空：如果此时动作结果还没有返回，已开始调用的写操作标记 `unknown`，不伪造未投递；已经取得的 `DispatchSent` 会保留。执行器迟到结果不能再次回复或把取消改成成功。取消不等于回滚已发出的页面或网络副作用，引擎排空前仍须由 Sessions 阻止人工输入和恢复。

## 验证

```sh
go test ./internal/codex ./internal/controller ./internal/harness -count=1
go test -race ./internal/codex ./internal/controller ./internal/harness -count=1
MUJICAGENT_BROWSER_SCHEMA_TEST=1 go test ./internal/codex -run '^TestInstalledBrowserToolSchema$' -v -count=1
```

最后一条只生成本机协议 schema 和读取版本，不启动模型会话，不产生模型用量。普通测试使用浏览器替身和假 App Server，覆盖声明、恢复拒绝、原 ID 文本/图片/错误回复、可信路由、重复/过期请求、停止/超时/关闭/断线、迟到结果以及活动数据最小化。
