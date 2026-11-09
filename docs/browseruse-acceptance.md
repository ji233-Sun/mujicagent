# BrowserUse 集成验收准备（#31）

本文件最初由 PR #105 提供验收准备，保留完整目标的用例矩阵。2026-11-09 用户已选择受限首版，后续实现与实际验证结论见 [运行说明](browseruse-runtime.md)。下表中的历史阻塞与“待执行”不是当前进度，也不能自动视为全部通过。协议依据见 [Codex 装配边界](codex-browser-tools.md)，执行语义见 [browser 契约](../internal/browser/README.md)。

## 原始开工条件（历史）

2026-11-09 核对主分支 `a0050f64956cac50a773638bb8e4d9a709d2b90c` 及 Issue 讨论：

| 前置 | 已知交付与缺口 | #31 开工判断 |
| --- | --- | --- |
| #26 公共契约 | PR #34 已合并 | 可复用 |
| #28 原生面板 | PR #98 已合并；CPU 布局与交互证据不覆盖真实 WebView、IME 和系统层级 | 保留原生验收项 |
| #30 Codex 接入 | PR #102 已合并；假 App Server 与协议测试不覆盖真实模型驱动页面 | 保留真实链路验收项 |
| #27 可行性 | 调查已关闭，但[报告](../WEBVIEW_FEASIBILITY.md)明确不通过完整 BrowserUse 的直接采用门槛 | 需 #25 明确范围或补足 API |
| #29 执行器 | Issue 仍开放，尚无已交付执行器 | 阻塞生产集成 |

按照 #31 的“前置完成前可准备验收用例，不能把准备工作视为完成集成”，本阶段仅维护测试页和本验收说明。开始修改 `main.go`、`controller.go` 前，重新读取 #25 的决策及 #29 的交付、原生运行证据；不能仅凭 Issue 关闭或 PR 合并推定验收通过。

待 #25 决定的受限方案来自 #27：只考虑主文档和可可靠引用的同源 iframe；所有网页内容和 Eval 返回均视为不可信；宿主秘密不进入 pageWorld；可信输入交给用户；跨域 iframe 自动化明确不支持；默认拒绝弹窗；跨源顶层导航由宿主显式决定。此处记录候选边界，不代表已采纳，也不放宽 #26 的敏感数据、取消排空和授权要求。若这些要求仍无法满足，应先补足引擎 API。

## 受控测试页

夹具为 [acceptance.html](../internal/browser/testdata/acceptance.html) 与 [acceptance-frame.html](../internal/browser/testdata/acceptance-frame.html)，只有静态 HTML/JavaScript，无依赖或产品运行服务。仅使用合成文本和合成密码，不填入真实凭据。

人工验收时从仓库根目录，在两个独立终端分别运行以下命令，各自保持运行并记录输出的随机端口：

```sh
python3 -m http.server 0 --bind 127.0.0.1 --directory internal/browser/testdata
```

将两个实际端口记为 A、B。在 App 内打开 `http://127.0.0.1:A/acceptance.html?peer=http://127.0.0.1:B`，将 A、B 替换为记录的端口；不同端口构成不同源。页面默认只加载同源 iframe，提供合法的其他 loopback 源后才加载跨源 iframe。验收后在两个终端按 Ctrl-C 停止服务。未来自动化用例复用现有 Go 测试设施和 `httptest` 动态端口，不依赖固定端口或固定休眠判定成功。

夹具包含以下独立观察点：

| 控件 | 可观察结果 |
| --- | --- |
| `Controlled text` / `Commit controlled text` | 只有 `input` 事件才更新 `Model`；点击提交显示 `Committed`，可揭示只给 DOM value 赋值的假成功 |
| `Form query` / `Navigate with form` | 实际 GET 导航到同一文件，返回文档显示 `Navigation complete: ...`；须验证导航代次 |
| `Start delayed change` | 先显示 pending，2 秒后增加计数；用于检查停止等待后仍可能出现的迟到页面变化 |
| `Trusted click only` | JS click 显示 rejected，原生用户 click 显示 accepted；拒绝不能记成目标成功 |
| `Save synthetic marker` | 保存已知 A/B Cookie 与 localStorage 标记，仅显示合成存储标记及 Cookie 是否匹配 |
| 两个 iframe、跨源链接、popup 链接 | 验证同源引用、跨源边界、导航决策与默认拒绝弹窗 |
| `Fixture password` | 合成敏感值用于检查 Observe、Capture 和活动持久化的拒绝或脱敏 |
| `Replace JSON.stringify` | 主动破坏 pageWorld 返回封装；最后执行并重新加载复原，不能作为权限或宿主安全判断依据 |
| `Bottom marker` | 主视口滚动结果；边界未移动时不能假报成功 |

## 完整目标验收矩阵

下表按实际运行证据逐项核对；界面入口、实际执行器或证据缺失时登记“阻塞”，不改为“通过”。受限首版的正向操作链路使用 [acceptance-simple.html](../internal/browser/testdata/acceptance-simple.html)，避免把同时包含密码及 iframe 的边界测试页当作可安全截图的页面。原始 `acceptance.html` 继续用于敏感数据及不支持能力验证。

| 用例 | 操作 | 必须观察到的结果 |
| --- | --- | --- |
| A1 打开与授权 | 新建对话，打开面板，以人工地址栏进入夹具；恢复前尝试 Agent 读取，再显式恢复 | 初始 Human、权限全 false；Agent 被拒绝；恢复后才可 Observe；Navigate、Interact、Capture 各自独立授权 |
| A2 完整链路 | 授予本例需要的权限；Open → Observe → Fill `fixture-run-A` → 重新 Observe → Click commit → 重新 Observe → Capture | UI 和工具使用相同 PageKey；页面 Model 与 Committed 均匹配；每次写入后重新取得引用；截图来自该页可见区域，无工具或截图占位成功 |
| A3 表单导航与滚动 | 填写合成 query，重新观察后提交表单；滚动主视口至底部，再尝试越界滚动 | 新文档显示匹配结果，Navigation 增加；旧引用 stale；实际偏移变化才成功，越界未移动返回 unconfirmed |
| A4 控件与敏感数据 | 先仅赋 DOM value 对比受控模型，再测试正常 Fill、密码 Fill、Observe/Capture | DOM 赋值不被误判为受控输入成功；密码 Fill 被拒绝；敏感数据不能进入模型结果、日志、活动、state.json；不能安全遮挡时明确失败 |
| A5 暂停与排空 | 启动 delayed change，在 pending 时暂停，排空前尝试人工输入、恢复和新 Agent 动作 | 立即撤权，后续 Agent 动作拒绝；Busy 期间人工输入与恢复禁用；允许迟到计数变化但不能提前解锁或把取消改为成功；安全排空后才人工接管 |
| A6 停止与关页 | 分别在 pending 时停止轮次、关闭网页、删除对话；清理后再新建页面 | 调用按原 JSON-RPC ID 至多回复一次；Closed 后不接收新动作；新 PageID 不复用；旧回调不覆盖替换页面或创建已删除对话的页面 |
| A7 收起与切换 | 收起活动面板，再显示；随后切换对话 | 收起保留页面并提示自动操作状态；切换立即取消旧页工具上下文并接管；不会把旧页面显示到新对话 |
| A8 两个临时会话 | 对话 A/B 打开同一源，分别保存标记 A/B；切换检查；关闭全部页面并重新创建，再重启应用检查 | 相同源下两对话各自只见自身标记；新临时 Session 无旧标记；重启不承诺恢复登录；只记录匹配结果，不收集原始 Cookie |
| A9 断线与重连 | 活动调用期间断开 Codex，重连后尝试恢复已有线程，再开始新线程 | 旧连接取消并不向新连接回写；执行器撤权且排空；不能确认已有线程工具注册时明确拒绝并保留历史；新连接独立注册与去重 |
| A10 引用与重复请求 | 测试过期 PageID/Navigation/ObservationID、重复请求 ID、同 callId 不同请求 ID及并行动作 | 不串页、不重复副作用/回复；过期或竞争动作明确 stale/busy；失败保留 dispatched/unknown，不能自动重试 |
| A11 页面能力边界 | 同源 frame 点击；跨域 frame 操作；trusted-only 点击；弹窗与跨源导航 | 同源仅在引用校验可靠时成功；不支持能力明确失败；受限导航与 popup 无静默放行；不转用外部浏览器、系统输入或 shell |
| A12 不可信结果与失败 | 最后启用 stringify 篡改，测试读取失败、加载失败和不支持工具 | 网页数据不能授权、修改宿主身份或访问 Go 私有绑定；不能保证安全返回时拒绝；错误可读且不泄漏原始页面异常，恢复后仍需显式授权 |
| A13 原生交互 | macOS 人工检查宽/窄窗口、地址栏/聊天输入、IME、设置与权限浮层、收起后再显示、关闭窗口 | 原生页面层级和焦点正确；地址 Enter 不发聊天；关闭无崩溃或悬挂工作；记录平台、人工步骤、截图与结果 |
| A14 展示与语言 | 切换 zh-Hans/en，检查六个工具的运行/成功/失败/取消活动 | 使用已有活动归并；名称与状态可读；不被原始动态工具快照覆盖；无填充值、页面正文、图片或未经处理异常持久化 |

A5/A6/A9 必须分别记录协议等待结束与底层排空完成。夹具的 2 秒延迟只制造迟到效果，不构成排空保证；应以宿主权威状态、实际页面结果及截止时间判断，不能用固定休眠替代执行器证明。

## 真实 Codex 验收

仅在前置完成、真实连接及模型用量另获授权后，由实际 Codex 会话执行 A2/A3。记录授权范围、所选模型及 CLI 精确版本。页面身份由可信宿主传入工具上下文，不能让模型猜 PageID、用当前选中对话代替路由，或从页面文本取得权限。

可使用以下任务内容，替换合成标记与本次允许的 loopback 地址：

> 仅使用应用提供的 browser 工具操作已授权的测试页。先用 browser_open 打开本次允许的 loopback 地址，再观察，填写 Controlled text 为 fixture-run-A，重新观察后点击 Commit controlled text，再观察并报告实际变化。完成表单导航和主视口滚动后，按已授予的权限分享当前可见区域。每次写操作后重新观察；过期、未确认或不支持时明确停止，不转用 shell、外部浏览器或系统输入。

真实模型回复需与 App 内页面及工具终态一致。假服务测试、原生夹具单独运行、历史 #27 截图均不能替代该项；未获授权登记“未执行”，保持 #31/#25 开放。

## 已有回归入口与后续验证

以下单元与协议回归使用页面替身/假服务；真实执行器和模型的独立入口见[运行说明](browseruse-runtime.md)，不能用这些回归替代原生验收：

```sh
CGO_ENABLED=0 go test ./internal/view ./internal/codex ./internal/controller -run 'TestBrowser|TestDynamicTool' -count=1
go test -race ./internal/codex ./internal/controller -run 'TestBrowser|TestDynamicTool' -count=1
```

UI 布局证据使用直接设置状态的 `TestBrowserLayoutScreenshots`，按 [MyGo 维护规则](../.agents/skills/mygo-maintenance/SKILL.md#verification) 生成并查看本次 CPU 离屏 PNG。当前集成已更新权限与工具摘要，并生成 40 个中英文/尺寸场景。原生窗口、IME、实际 WebView 键盘释放和 GPU 仍需单独记录。

实际集成改变代码后，以[当前 CI](../.github/workflows/ci.yml)及 #31 要求执行并保存结果：

```sh
go tool mygo generate
git diff --exit-code -- locales/catalog_gen.go
go tool mygo vet .
CGO_ENABLED=0 go test ./... -count=1
go test -race ./internal/harness ./internal/codex ./internal/controller -count=1
go test -race ./internal/browser -count=1
go vet ./...
go tool mygo i18n check
go tool mygo build -platform "darwin/arm64,darwin/amd64"
```

这组命令是后续执行清单，未在准备阶段宣称通过。只编译契约的 browser 包不等于执行器测试；双架构构建不等于双架构运行验证。原生应用使用独立的 `MUJICAGENT_DATA_DIR` 和临时 MyGo Session，避免读取真实会话；隔离数据目录不隔离 CLI 登录或取消模型费用授权要求。

## 验收记录

2026-11-09 当前实现的核对结果如下；更具体的命令、范围及安全限制见[运行说明](browseruse-runtime.md)。这些结果不改变下方原始模板的用途。

| 用例 | 当前证据与剩余项 |
| --- | --- |
| A1 | 生命周期、权限及 UI 回归通过；新页 Human、权限默认关闭；支持扩展的 macOS 提供独立 Capture 授权 |
| A2 | 原生主文档及真实 Codex 六工具链路通过，含实际视口 PNG 回传 |
| A3 | 原生实际表单导航、代次增长、旧引用拒绝和滚动通过 |
| A4 | 文本敏感保护、30 次独立脱敏位图对比与 10 类复杂内容拒绝通过；不承诺识别任意无标识秘密 |
| A5 | 原生截图实际在途取消的 race 通过：立即撤权、Busy 持续至排空、不返回 PNG；人工输入过程未验收 |
| A6 | 原生截图中关页/导航及旧代次拒绝通过；停止/删除的完整原生人工流程未验收 |
| A7 | CPU 收起状态及原生恢复后切换撤权通过；原生层级仍未人工验收 |
| A8 | 同源双临时 Session、关页重建的 Cookie/localStorage 隔离通过；未执行完整应用重启人工流程 |
| A9 | 既有协议断线/注册/去重回归通过；原生活动期间断线与重连未完整验收 |
| A10 | 协议去重及页/观察引用的单元、race、原生 DOM 变化回归通过；Capture 保持原有效引用，截图中导航返回 stale |
| A11 | 主文档限定；iframe、弹窗和可信输入不提供自动化，不视作完整能力通过 |
| A12 | Go 绑定拒绝、隔离 world 抵抗 main world 序列化/原型篡改、复杂页面无 PNG 拒绝通过；非扩展平台保留不可信返回边界 |
| A13 | 未执行人工焦点、IME、系统窗口层级检查，阻塞 |
| A14 | 中英文 CPU 工具状态、注册错误本地化和活动脱敏回归通过；不代表原生输入验收 |

每次执行复制本模板，结果填写通过/失败/阻塞/未执行，附可访问证据。协议原始正文、填充值、Cookie 和未脱敏截图不写入持久日志或 PR；证据仅使用上述合成页面数据。

| 字段 | 记录 |
| --- | --- |
| 被验收提交、#25 范围决策与 #29 交付 | 待填写 |
| 日期、macOS build、架构、Go/MyGo/CLI 版本、模型 | 待填写 |
| 数据隔离目录、测试源、窗口尺寸、语言 | 待填写 |
| 用例 ID、步骤、预期与实际页面结果 | 待填写 |
| 工具终态、错误分类、投递状态、取消与排空顺序 | 待填写 |
| 证据类型与附件链接（CPU/原生网页/原生窗口/真实模型） | 待填写 |
| 结果、剩余问题、清理结果与运行验证限制 | 待填写 |

只有 #31 的全部验收完整满足后才使用关闭关键词，并回填 #25；本准备材料不关闭任何集成或总任务 Issue。

## 剩余人工原生验收

当前 #29 执行器已有实际六操作、隔离、取消与导航证据，可由交付 PR 在合并后关闭。#31/#25 继续开放。以下流程必须由人操作，不能用 CPU PNG、DOM API 或桌面自动化替代：

1. 按前文启动 loopback 夹具；构建 helper 后使用隔离的 `MUJICAGENT_DATA_DIR` 启动应用。只访问合成页，不填写真实凭据。
2. 在宽/窄窗口中以人工键鼠切换地址栏、聊天输入和网页输入。用中文 IME 组合并确认文字；地址栏 Enter 只导航，不能发送聊天。
3. 打开权限弹窗和设置，检查原生网页没有遮挡浮层；关闭浮层后焦点正确。收起、再展开网页，检查层级和尺寸。
4. 在 Agent 操作期间暂停，核对立即撤权、排空前不能输入或恢复；排空后人工输入/点击生效，再显式恢复。
5. 连续执行停止、关页、切换对话、删除及断线重连；核对不串页、旧结果不覆盖新页、权限不自动恢复。
6. 两个对话在同一源分别保存合成标记；关页重建及重启应用后确认临时 Session 不保留旧登录。
7. 记录 macOS build、架构、步骤、结果和原生窗口截图。发现失败保留 Issue；全部满足后再更新 #31/#25，不能提前使用关闭关键词。
