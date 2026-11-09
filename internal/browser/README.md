# BrowserUse 公共契约

公共语义定义于 [contracts.go](contracts.go)；[runtime.go](runtime.go) 实现 #29 的临时页面、互斥、授权、引用与取消生命周期，[mygo.go](mygo.go) 和固定的 [page.js](page.js) 连接现有 MyGo WebView。入口使用 `New(window, changed)`，UI 和 Codex 共用同一个 Runtime，原生页面由 `View(PageKey)` 取得。

用户于 2026-11-09 明确采用 #27 的受限首版，并授权隔离的本地合成页面进行真实 Codex 验收。具体能力、使用步骤与实际验证记录见 [运行实现](../../docs/browseruse-runtime.md)；原[集成验收矩阵](../../docs/browseruse-acceptance.md)保留完整目标与待核对项。历史可行性报告的 pageWorld、可信输入及弹窗限制仍然成立。

macOS Capture 已通过系统 WebKit 进程内扩展补足可信边界：独立 world 的固定策略与同步原生 snapshot 在同次调用中执行，再经私有 IPC 返回；不使用 pageWorld 的安全声明。无法可靠检查的复杂内容返回 `ErrSensitive`，不具备扩展的平台返回 `ErrUnsupported`。详情见[可信截图实现](../../docs/browseruse-trusted-capture.md)。#29 执行器已有原生与真实六工具证据；#31/#25 剩余人工流程已由需求方明确豁免，未执行项不记为通过。

## 下游入口

| 调用方 | 入口与责任 |
| --- | --- |
| #28 原生面板 | `Sessions` 创建、读取状态、接管、显式恢复、关闭；地址栏通过 `Tools.Open` 发送 Human 请求。只在 `Human && !Busy && !Loading` 时开放原生键鼠输入 |
| #29 执行器 | 实现 `Tools` 六个操作和 `Sessions`，负责真实效果、按页面串行、权限、引用校验、脱敏和资源销毁 |
| #30 Codex 接入 | 仅暴露 Open/Observe/Click/Fill/Scroll/Capture；固定 ActorAgent，从已认证调用上下文解析本地 ThreadID；保留失败结果的 Dispatch，不按 nil 数据或 JS 返回成功推断效果 |
| #31 装配 | 将本地 `model.Thread.ID` 与 `ServerID` 映射；按 PageKey 给 UI 注入对应原生页面渲染回调，切换对话前接管旧页；不将 MyGo 对象或渲染回调放进工具参数/结果 |

`contracts.go` 不依赖 MyGo 或 Codex。UI 的原生渲染回调在视图边界定义并由装配提供，不从 Tools 反射/断言底层 WebView。状态通过 `Sessions.Status` 取得（宿主按需刷新），结果是脱离内部可变状态的快照；不在本任务新增事件总线。以上都是内存 Go 接口；Codex JSON schema、图片内容编码由 #30 根据实际协议定义，不直接序列化整个 Go 结构。

## 标识与引用

- ThreadID 始终是本地对话 ID；不得默认使用当前选中对话，也不得直接把 ServerID 当 ThreadID。每个线程最多一个活动页、一个独立临时 Session；Create 重入返回现有状态，关闭后重新 Create 得到全新 PageID 和空 Session。不同对话不共享 Cookie/存储，不导入系统浏览器数据，不跨重启保存登录。
- PageID、Navigation、ObservationID、元素 ID 均由宿主生成，不信任网页提供的相同字段。PageID 在进程生命周期内不复用；PageRef.Navigation 从 1 开始，0 非法。加载初始空页后的所有导航尝试（含重载、历史、同文档 URL 变化和失败/取消的导航）都递增，重定向链允许多次递增。改变代次、清空引用必须发生在接收后续动作之前。
- Observe 成功产生新的 ObservationID，替换前次观察。元素引用只属于该次观察与该 PageRef；下一次 Observe、任意写操作受理、导航、DOM 变化、接管、恢复和关闭都会使之失效。Capture 本身不使引用失效。一个引用最多用于一个写操作；受理前核验请求与元素的 ThreadID/PageID/Navigation 完全一致、观察仍为最新、元素仍连接且身份未变。
- 不能可靠监测身份或 DOM 变化时返回 ErrStale/ErrUnsupported，不重新用相同 selector 猜测目标。Agent 不能提交任意 JS、CSS selector、XPath 或原生句柄。同源 iframe 只有在执行器能遵守相同引用规则时可提供元素；跨域/可信事件等能力不得默认为支持。

## 状态、单写者与取消

| 条件/操作 | 状态与效果 |
| --- | --- |
| Create | 初始 Human，权限全 false；页面可交给用户，不自动授予 Agent 控制 |
| 用户 Resume | 仅 Human 且 `!Busy && !Loading` 有效；替换权限并进入 Idle。没有模型可调用的恢复入口 |
| Idle 接受 Agent 操作 | 进入 Agent、Busy=true，连 Observe/Capture 也按同一页面互斥；结束后回 Idle，除非已经接管或关闭 |
| Human 接受 UI 工具操作 | 保持 Human，Busy=true，临时禁用原生输入；操作结束解除 Busy。Agent 请求均拒绝 |
| TakeOver/停止/断线/切换对话 | 立即进入 Human，撤销权限、取消当前 Agent 上下文、失效引用；已发出的工作仍可能 Busy。安全排空后才解锁人工输入 |
| Close | 立即进入 Closed，拒绝后续工具动作；取消并销毁页面和 Session。排空完成才令 Busy=false；相同 PageKey 重试关闭可完成未完清理 |
| 面板收起 | 仅改变可见性，不隐式停止、恢复或销毁；必须继续提示当前 Agent 状态。切换对话是另一事件，必须接管旧页，避免隐藏页继续操作 |

每个页面同一时刻只执行一个 Tools 调用，不排队，竞争者返回 ErrBusy。其他页面可独立执行。原生人工输入与工具动作共用互斥边界；用户尝试在 Agent 模式操作网页时，先走 TakeOver，不能先投递输入再取消 Agent。

TakeOver/Close 优先于普通工具调用，不能因为 Busy 就跳过撤权。TakeOver 成功返回时旧任务已排空；若等待超时，仍保持 Human、Busy=true、权限撤销，用户输入和 Resume 都不可用，直到底层已安全排空或 Close 销毁隔离页面。仅“停止等待 JS 回调”不算排空；无法保证没有迟到写入时不能解锁给下一操作者。没有底层可取消能力必须明示失败，不能用系统级输入绕开。

生命周期方法本身也串行化：Close 优先且终态；关闭开始后 Resume 不能成功。使用旧 PageKey 的关闭、接管、恢复不能影响同一对话新建的页。Create/Status 只接受可信宿主的本地 ID；Status 对不存在的会话返回 ErrNotFound，保留最近 Closed 快照直到下一次 Create。Closed 且 Busy 时 Create 返回 ErrBusy，必须等旧资源销毁完毕才能新建。TakeOver 对已排空的 Human 页幂等，对 Closed 页返回 ErrClosed；Resume 在非 Human 状态返回 ErrPermissionDenied，Human 但 Busy/Loading 时返回 ErrBusy。

所有调用使用 `min(ctx deadline, 调用开始 + DefaultTimeout)`；取消使用 `context.Canceled`，超时使用 `context.DeadlineExceeded`，支持 `errors.Is`。取消前若已发出动作，返回结果必须保留 DispatchSent（无法判断则 Unknown）。已受理的 Tools 调用超时/取消后停止后续投递并撤销 Agent 控制，进入 Human 排空（已 Closed 则保持 Closed）；恢复必须用户明确触发。未受理的调用和只读 Status 查询取消不改变页面状态。生命周期撤权一旦生效，不因其等待被取消而撤销；Resume/Create 在提交状态前取消则不生效。已发出的页面请求、表单提交和服务端副作用不可回滚，排空也不等于撤回。以 PageKey、导航代次及任务身份丢弃过期完成通知，不能由迟到结果把 Human/Closed 改回 Idle。Open/Click 自身发起的导航由执行器关联到原任务并返回新代次；无关导航导致原采样失效，返回 ErrStale。

## 六个操作与完成标准

| 操作 | 输入与权限 | 完成依据 |
| --- | --- | --- |
| Open | 当前 PageRef、绝对 http(s) URL；Agent 需 Navigate | 新页面实际提交且可观察，返回最新代次与实际到达的脱敏地址；重定向也须授权，单纯调用 LoadURL 不算完成 |
| Observe | 当前 PageRef；已 Resume 的 Agent 可用 | 返回当前可见内容的脱敏、有界快照和新引用；加载中或采样期间发生导航/DOM 变化则失败，不拼接不同代次数据 |
| Click | 当前观察的 ElementRef；Agent 需 Interact | 返回具体已观察变化（导航、控件状态或可见内容变化）；事件派发本身不够，不能承诺付款/提交等业务成功 |
| Fill | 当前可填元素引用、替换全文 Text（可为空）；Agent 需 Interact | 读回值符合且页面实际消费了输入；若无法确认受控组件生效则 ErrUnconfirmed，不回显 Text。已经满足目标且确认状态时允许 DispatchNone + Observed |
| Scroll | 主视口 DeltaX/DeltaY，有限非零 CSS 像素；Agent 需 Interact | 读回实际偏移变化；边界未移动时 ErrUnconfirmed。嵌套容器滚动不包含在本轮契约 |
| Capture | 当前 PageRef；Agent 需 Capture | 本页可见视口、脱敏后的有效 PNG 和像素宽高；不含其他应用/页面、桌面或整个长页 |

所有 Tools 请求都须有非空身份和合法 Actor；Actor 只由宿主指定。Human 请求仅来自用户操作，不能由模型选择以绕过权限。未明确授予的 Agent 写操作和截图全部拒绝；Agent 的 Observe 也仅在 Idle 可执行。Human 的六种调用均要求 Human 且非 Busy。加载中拒绝新 Tools 调用（ErrBusy），原调用可等待自身导航达到完成条件。输入、状态与权限在实际投递前再次校验。

`ActionResult` 即使 error 非 nil 也必须读取。DispatchNone 表示确定未投递；DispatchSent 仅代表已投递；Unknown（含零值）不可安全重试。CompletionObserved + 非空、脱敏 Evidence 才能返回 nil error；零值/Unconfirmed 一律不是成功。证据只说明观察到什么，页面本身仍不可信。没有可确认变化时返回 ErrUnconfirmed，不能编造证据。Observe/Capture 失败时不消费部分数据；ActionResult 失败时只使用投递状态和仍有效的页面身份，不能把旧 Evidence 当当前状态。

控制器按原始工具请求 ID 保证至多一次投递/回复；重复请求不得重跑动作。该去重属于 #30，不在公共接口另设第二套 ID。执行器拒绝过期引用；Unknown/Sent 的失败不可自动重试，必须重新观察并重新决策。

## 权限、数据与错误

Navigate/Interact/Capture 是宿主明确授予的独立权限，不从 Codex 的 shell 权限档位、网页文本或重定向自动继承。新源导航、下载、上传、外部协议、弹窗、凭据输入和其他超出用户授权的行为默认拒绝，不能用一次 Interact 授权解释为任意交易/提交许可。具体业务审批由可信宿主在投递前完成；这组 bool 不替代审批。

所有返给 UI/Codex 的结构化内容、错误和日志必须排除 Cookie、Authorization、令牌及密码值：不返回 storage、网络头、原始 HTML/script、input value 或原始引擎异常；显示 URL 移除 userinfo、query、fragment，并对 path/title/text/元素名称中的敏感值脱敏。有疑义无法保证安全时返回 ErrSensitive，而非原样透传；保留安全的 PageKey、错误分类和投递状态。Fill 对 password、凭据及未知敏感字段拒绝，交给原生人工接管输入，人工填写值不得再由观察或截图回传。

Capture 授权前 UI 必须说明“将分享当前页面可见区域”，并展示/确认范围；截图在受控内存中处理、遮挡凭据/令牌/密码区域后才成为 Screenshot 结果。无法可靠遮挡时返回 ErrSensitive/ErrUnsupported，不能因用户已授权截图就泄漏明确禁止返回的秘密。其他可见内容也可能敏感，默认不写文件、不进日志，只有显式 Capture 授权才送模型。原生网页直接显示与工具返回脱敏结果是两个边界。

错误用 `errors.Is` 匹配 [contracts.go](contracts.go) 的 sentinel：参数非法 InvalidArgument；未登记/跨线程页面 NotFound（不泄漏其他线程信息）；当前已关闭页 Closed；导航/观察/元素过期 Stale；互斥/加载/排空 Busy；权限或操作者错误 PermissionDenied；能力缺失 Unsupported；无法安全返回 Sensitive；已投递但未确认目标 Unconfirmed；其余底层故障 Engine。context 取消/超时使用标准错误，禁止把原始异常文字或输入值拼进错误。空观察/空图片不能充当成功的 Unsupported 替身。

状态机回归位于 `runtime_test.go`，使用可控的底层等待验证取消后 Busy 不提前解除、关闭优先、旧页面与引用拒绝、显式授权及隔离。原生网页和 Codex 验收复用 `internal/view/browser_native_test.go`，须按运行文档显式开启；普通测试不启动原生窗口或真实模型。
