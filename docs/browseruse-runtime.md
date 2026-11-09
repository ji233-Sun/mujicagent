# BrowserUse 受限实现与验收缺口

2026-11-09，需求方选择采用 #27 的受限方案，并明确授权使用本机 Codex 登录与模型用量，仅在隔离的本地合成网页上验收。继续使用 MyGo v0.7.4，没有升级依赖、引入其他引擎或独立服务。#29 的执行器与 #31 的装配在同一交付分支实现；PR #105 的四个文件已逐文件审查，未发现需修复的问题，验收材料已纳入。

**这不是 #29/#31/#25 完成声明。** 安全审查确认：网页可伪造 MyGo 的外层 Eval 序列化，把敏感检查改为合法的 `{"code":"ok"}`。原生截图因此统一返回 `unsupported`，不释放任何 PNG，不用页面返回作为脱敏保证。完整截图链路及 macOS 人工原生验收仍阻塞这些 Issue；普通受控页曾成功截图并不能消除该缺口。

## 实现与使用

`browser.New` 为原生 UI 窗口创建 Runtime，`main.go` 将同一个实例交给 Controller 和 BrowserPanel。每个本地 ThreadID 对应一个临时 Session 和 PageKey。Go 状态不含原生元素或 Context；原生句柄只用于显示，数据不进入应用持久化。

新页面默认 Human，权限均关闭。显式恢复后可 Observe，Navigate/Interact 独立授权。Capture 契约仍保留，但 UI 禁用该权限，原生执行器不提供此能力。跨源顶层导航只由人工地址栏授权；网页回调不能扩大授权范围。历史导航即使绕过 `OnWillNavigate`，也会在原生提交或 URL 刷新时撤权；必须重新从人工地址栏打开该源才能恢复。模型参数不能指定 Actor、ThreadID、CSS selector、JS 或原生句柄。外部页面设置 `Untrusted: true`，保留默认拒绝弹窗、下载和权限请求的策略。

操作按页面互斥、不隐式排队。观察创建宿主随机的临时引用；下一次观察、写入、DOM 变化、导航、接管和关闭会使引用失效。UI 和工具取得同一页面对象；同源 iframe 首版也不提供可操作引用，不宣称支持其 DOM 自动化。

停止等待与引擎排空分开。取消立即撤权并保持 Busy；原生同步 Eval 实际返回后才解锁。Eval 本身超时而无法确认排空时销毁页面作为隔离屏障。已提交的网页或网络副作用不可撤销。关闭采用终态，旧 PageKey 不能操作重建页面；连接替换、停止、删除对话和切换对话均接入撤权。

Codex 的动态工具只在具备浏览器页面的新线程中注册；同连接已注册线程可继续使用。旧线程、磁盘恢复或重连后的线程不能假称已补注册。每轮宿主上下文只补充 PageID/Navigation 与固定能力说明，不带网页内容或权限秘密。显式恢复可重新绑定仍运行的轮次，已取消请求保留原上下文和去重标记。

## 能力与安全边界

| 能力 | 行为 |
| --- | --- |
| 主文档观察 | 仅可见文本及支持控件，输出有界；不返回 HTML、storage、Cookie、headers 或 input value |
| 点击/填写 | 普通 DOM 事件；检查可见变化及字段读回值，无法确认返回 unconfirmed；不承诺业务成功 |
| 滚动 | 主视口 CSS 像素；实际偏移未变化返回 unconfirmed |
| 截图 | 原生执行器始终返回 unsupported，不产生或回传图片；可信检查与原子脱敏边界尚缺 |
| iframe、弹窗、上传/下载、可信事件 | 不提供自动化能力；请人工接管 |
| 绑定隔离 | 外部页面不能调用应用 Go 方法 |
| pageWorld | 页面及 Eval 返回仍不可信；非隔离 world。格式异常返回安全错误，不用于宿主权限或页面身份判断 |
| 临时 Session | 按对话隔离；关闭最后页面销毁会话，不跨重启保留登录 |

凭据字段与常见认证格式会被过滤，Go 侧复用 MCP 的文本脱敏策略，覆盖 Basic/Bearer、凭据赋值、Cookie、私钥和 URL 凭据；页面中的短密码回显及 textarea 名称取值有原生回归。没有字段标识的任意秘密不能保证自动识别。不要向自动化网页提供真实凭据。截图授权不能替代安全边界；不能安全处理时必须失败。原生输入法、系统窗口行为和 GPU 不由 CPU 截图证明。

## 验收入口

普通回归不启动原生网页或模型：

```sh
go test -race ./internal/browser -count=1
CGO_ENABLED=0 go test ./internal/controller ./internal/codex ./internal/harness ./internal/view -count=1
go tool mygo vet .
```

macOS 原生网页验收使用真实产品 View、Runtime 和临时 Session，由测试直接设置场景，没有模拟键鼠：

```sh
MUJICAGENT_BROWSER_NATIVE_TEST=1 \
go test ./internal/view -run '^TestBrowserNative(Runtime|ResumeThenSwitch)$' -v -count=1 -timeout 90s
```

真实 Codex 验收另行开启，会使用本机登录与模型用量。验证五个工具成功与截图工具明确失败，不将失败改成完整截图验收通过。夹具由 `httptest` 仅在 loopback 随机端口提供，退出后清理：

```sh
MUJICAGENT_BROWSER_NATIVE_TEST=1 MUJICAGENT_BROWSER_LIVE_TEST=1 \
go test ./internal/view -run '^TestBrowserNativeCodex$' -v -count=1 -timeout 5m
```

截图布局场景通过 `ui.NewTester` 直接设置状态：

```sh
MUJICAGENT_BROWSER_SCREENSHOTS="$PWD/build/browseruse-acceptance/cpu" \
CGO_ENABLED=0 go test ./internal/view -run '^TestBrowserLayoutScreenshots$' -count=1
```

## 本次实际记录

- 环境：macOS 27.2，arm64；Go 1.27.3，MyGo v0.7.4，Codex CLI 0.167.0。
- 原生受控页：打开、观察、受控输入、点击提交、实际 GET 表单导航、旧文档/DOM 引用拒绝和实际滚动通过。
- 隔离与拒绝：相同源的双 Session Cookie/localStorage 隔离、关页重建空 Session、私有 Go 绑定拒绝、pageWorld 序列化异常失败、关页后请求拒绝、跨源历史返回撤权、显式恢复后切换对话撤权通过。
- 敏感保护：短密码及 Basic/Bearer 回显被过滤；普通及包在 label 内的 textarea 初始值不进入元素名称；伪造合法截图检查返回仍被拒绝且不返回 PNG。
- CPU：中英文 840×560、1440×900 的人工/Agent/加载/排空/错误/收起/权限/未连接及工具记录场景，共 36 张；已查看工具记录 PNG，确认工具名、结果分类及错误使用中英文文案。
- 真实 Codex：`gpt-6.1-sol`，最终运行 65.83 秒，通过五个工具与截图明确拒绝的断言，产生 11 条活动记录。曾通过的截图正向试验已被安全审查否定，不能用作当前实现支持截图的证据。
- 完整检查：生成及生成后无新增差异、MyGo vet、`CGO_ENABLED=0 go test ./... -count=1`、harness/codex/controller/browser race、Go vet、i18n，以及 macOS arm64/amd64 构建均通过；本地仅 arm64 原生运行。
- 未验证：人工键鼠与 IME、系统原生窗口层级、GPU 正确性，以及 Windows/Linux 原生运行；CPU 布局及真实 WebView DOM 验证均不覆盖这些项目。#31/#25 的完整目标仍须按相应验收要求判断，不能仅凭受限首版代码关闭。
