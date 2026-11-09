# MyGo 0.7.4 BrowserUse 可行性验证（#27）

## 2026-11-09 实现阶段补充

需求方已选择受限方案，并授权隔离本地合成页面的真实 Codex 验收。产品装配与后续证据见[运行说明](docs/browseruse-runtime.md)，以下原始可行性结论继续保留。

执行器安全审查确认，网页不只能破坏序列化格式，也能伪造完全合法的 Eval 成功结果。因此不能用 pageWorld Eval 做可信敏感检查。后续实现已通过 MyGo 原生互操作钩子和 WebKit 进程内扩展补足受限截图路径，真实 Codex 六工具与原生位图回归通过。历史返回另有绕过 `OnWillNavigate` 的路径，当前实现增加原生提交/URL 刷新的源校验与撤权。需求方于 2026-11-09 明确豁免剩余人工原生验收，授权按现有证据合并关闭 #31/#25；未执行项不计为通过。

新增原生反例确认 `WKPreferences.javaScriptEnabled=false` 仍允许已发起 fetch 的 Promise 修改 DOM，不能据此认为页面已经冻结。当前方案不依赖脚本开关，而在同一次进程内调用中脱敏和栅格化。反例、实现及系统版本限制见[可信截图说明](docs/browseruse-trusted-capture.md)。以下「决策」及后文是 #27 原始调查的历史记录，不代表补丁后的当前能力。

## 决策

**结论：不通过“直接采用现有 MyGo 实现完整 BrowserUse”的门槛；受限的主文档 DOM 原型可行。**

原生嵌入、观察、填写、点击、截图和临时会话隔离均已实测成功。关闭 Go 绑定不妨碍宿主执行脚本，但 **`Untrusted` 不提供隔离脚本世界**：页面可篡改宿主观察的返回值。若“安全脚本执行”仅指不向网页开放 Go 权限，该项通过；若包括观察脚本和返回通道不受网页篡改，该项不通过。本报告不把这两个保证混为一谈，也不据此解除 #29 的合并前提。

由 #25 明确选择：接受以下受限范围后再推进 #29，或补足引擎 API。没有修改产品代码、升级依赖或引入其他运行时。

可接受的最小范围：主文档及同源 iframe 的 DOM 操作；每次操作后重新观察；网页内容和 Eval 返回均视为不可信数据；无任何宿主秘密进入页面脚本；可信输入交给用户；默认拒绝弹窗；跨源顶层导航由宿主显式决策。不能承诺任意网页、框架、跨域 iframe、验证码、可信事件或原生关联弹窗都能自动操作。

## 环境与证据级别

- 2026-11-06，macOS 27.2（26B123），arm64，Go 1.27.3，MyGo v0.7.4，原生 WKWebView，屏幕 DPR 2。
- 基于 `origin/main` 的 `0772363`；直接读取锁定模块源码。Context7 当前文档仅辅助查找，版本判断以 v0.7.4 源码为准。
- 临时验证程序位于 `/tmp/mujicagent-issue27.VPmbek/`；窗口采用与主程序一致的 `Content`、隐藏内嵌标题栏和 sidebar vibrancy，1140×780。原生工具栏与 `ui.WebView(...).Grow(1)` 同处一个窗口。
- 两个 Go HTTP 服务仅监听 `127.0.0.1` 随机端口。主测试运行源为 `http://127.0.0.1:61623` 和 `http://127.0.0.1:61624`；不同端口构成不同源。未连接 Codex、在线模型或真实网站。
- 下表“实测”指原生进程中的返回数据、网页最终状态或截图；“源码”不代表已覆盖所有运行场景。

## 支持矩阵

| 场景 | 结果与证据 | 执行器边界 |
| --- | --- | --- |
| 原生嵌入 | 实测成功，原生工具栏下显示网页，非独立浏览器窗口 | 使用 `Window.NewWebView` + `ui.WebView`；宿主 `Window.Page()` 为 nil |
| 布局与缩放 | 实测 `innerWidth/innerHeight` 从 1140/726 变为 900/596；窗口 900×650，再设置 1.25 倍页面缩放后为 720/476；截图确认裁剪和滚动 | 坐标需区分窗口、WebView、CSS 像素和 DPR |
| 观察、脚本 | 实测返回标题、尺寸、DOM 和 Promise 结果；`EvalContext` 可用 | 表达式直接返回；语句体必须显式 `return`，否则成功但结果为 null |
| 普通链接 | `.click()` 后 URL 为 `/next?q=link`，新页面文本包含 `Navigation complete` 和 `link` | 以新文档状态为成功依据，不能只看 click 无异常 |
| 普通表单 | 设置普通 input 并点击 submit，实际导航到 `/next?q=form-value`，服务器页面回显 `form-value` | 未测试文件选择、特殊输入类型或真实站点校验 |
| 受控输入 | 仅赋值后 `[value,model,text]` 为 `[raw,"",empty]`；原生 value setter + 冒泡 input 后三者均为 `agent input`，截图吻合 | 使用自建受控组件，不是 React/Vue 兼容性认证；必须读回业务状态 |
| 同源 iframe | 实测可取 `contentDocument`，点击 frame button 后文案变为 `frame-clicked` | 可递归同源 DOM；重导航后旧引用失效 |
| 跨域 iframe | 实测两个 frame 均显示；读取跨域 frame 的 document 抛 `SecurityError` | 现有 Page.Eval 无 frame 参数，不能宣称支持跨源 frame DOM 自动化 |
| 需要可信事件 | JS `.click()` 得到 `isTrusted=false`，页面保持 `blocked` | 没有通过 JS 伪造可信事件的路径 |
| 人工键鼠接管路径 | 用桌面工具向原生窗口输入 `human takeover` 并点击按钮，input/click 均为 `isTrusted=true`，模型同步，状态为 `accepted` | 验证了原生键鼠路径；不是人工操作录像，也不是 MyGo 提供了 WebView 专用输入 API。系统输入不得作为后台仅控制网页的替代实现 |
| 临时 Session | 同一 Session 第二个 WebView 读到 cookie `probe=A`、localStorage `A`；另一临时 Session 为 `""/null`；最后页面关闭后复用原 Session 也是 `""/null` | Cookie 和 localStorage 已实测；IndexedDB、缓存、Service Worker 隔离只获源码支持，未分别实测 |
| 禁止 Go 绑定 | `Untrusted:true` 下调用已注册的 `Probe.Touch` 明确拒绝，Go 调用计数一直为 0；同时宿主 Eval 成功 | 关闭绑定可行，不代表页面 Eval 是可信执行环境 |
| 跨源顶层导航 | 无 handler 时跨源链接不离开原页；安装精确 URL 允许规则后实际到达另一端口的 `/next?q=cross` 并回显 | 安装任何 `OnWillNavigate` 都接管默认策略，必须主动拒绝其他 URL；`LoadURL` 不经过该回调，调用前单独校验 |
| 弹窗默认拒绝 | Untrusted 无 handler 时点击 `_blank` 未出现新窗口 | 保留拒绝默认值 |
| 弹窗显式允许 | handler 返回 `Page.Untrusted:true` 的 WindowOptions 后出现原生窗口，但持续空白；第二次 `window.open` 返回非空引用，子页 URL 实测为 `about:blank`、body 为空 | **当前路径不支持正确加载关联弹窗**；不能把非空 window 引用当成功。最小复现见下文 |
| 关闭与迟到结果 | 运行延迟 2 秒的 Promise，100ms 后 Destroy，等待返回 `JavaScript execution returned a result of an unsupported type`；之后 Eval 返回 `mygo: window has been destroyed`；重开页面正常 | 不能依赖统一的取消错误文本；必须有超时、关闭标记和导航代次，丢弃迟到结果 |
| UI 线程 | 实测 HTTP worker 调用 NewWebView/Eval/CapturePage/Destroy/SetSize 成功；通过 `Window.Update` 发布重开后的显示对象 | 源码方法内部转主线程；构建函数和生命周期事件在主线程。不能在 UI 构建中启动重复任务或保存 ui.Element |
| 截图 | `WebView.CapturePage()` 返回 PNG，141589 字节，已解码并目视确认输入、frame、可信事件状态 | 页面截图不含原生工具栏；用它而非宿主截图证明网页状态。未验证全页长截图 |

## 安全和弹窗的最小复现

下列片段用于隔离验证程序，不是新增产品接口。先创建 `Content: ui.View(...)` 的宿主窗口，在 UI 中放置该 WebView：

```go
session := mygo.NewEphemeralSession()
v, err := win.NewWebView(mygo.WebViewOptions{
    URL: fixtureURL,
    Page: mygo.PageOptions{Session: session, Untrusted: true},
})
// 检查 err；在 Content 中渲染 ui.WebView(c, v).Grow(1)。
```

绑定隔离复现：注册仅增加原子计数的 `Probe.Touch`，调用
`mygo.call("Probe.Touch").then(x => x, e => e.message)`。返回
`this page is untrusted (PageOptions.Untrusted) and may not call Go methods`，计数为 0。

观察通道篡改复现，分两次 `Page.EvalContext` 执行：

```javascript
window.savedStringify = JSON.stringify;
JSON.stringify = () => window.savedStringify({ok:true,v:"tampered"});
return "original";
```

```javascript
document.title
```

第一次返回 `original`，后续读取标题返回 `tampered`。重新加载文档后标题恢复。v0.7.4 的 `bridge.go` 使用页面 `JSON.stringify` 封装结果，`internal/darwin/window.go` 使用 `WKContentWorld.pageWorld`，没有暴露隔离 world 或指定 frame 的 Eval API。页面可执行同样的覆盖；不能将返回值用作授权、来源校验或宿主安全决策。这个实验没有证明页面能绕过 Go 绑定拒绝。

弹窗复现：

```go
v.Page().SetWindowOpenHandler(func(req mygo.WindowOpenRequest) *mygo.WindowOptions {
    return &mygo.WindowOptions{
        Title: "Issue 27 popup", Width: 500, Height: 300,
        Page: mygo.PageOptions{Untrusted: true},
    }
})
```

```javascript
window.child = window.open("/next?q=popup2");
return !!child; // true，但不是加载成功
```

稍后读取 `[child.location.href, child.document.body.innerText]` 得到
`["about:blank", ""]`，原生截图也为空白。源码 `windowHandler.NewWindow` 清空返回 options 的 URL，由 WebKit 自己加载，并继承 opener Session；`WillNavigate` 又默认拒绝 Untrusted 页跨源导航。**怀疑**初始空源与导航策略冲突，未修改上游验证根因。需要上游支持在首次加载前安装子窗口策略，或由宿主拒绝原生弹窗后显式新建页面（后者不保留 opener 语义，须先明确缩减范围）。不得用关闭 Untrusted 解决。

## 可行操作路径与必须补足的部分

1. 为任务创建临时 Session；所有不可信页面和将来创建的子页面显式设置 Untrusted。宿主 LoadURL 前校验目标；导航 handler 精确匹配允许的目标，其他调用 PreventDefault。该回调只管顶层导航，不是子资源请求的网络隔离措施。
2. 用宿主保存的 WebView 句柄执行固定脚本模板，通过 JSON 编码传入数据。观察输出按不可信输入处理并限制大小；不允许页面指令或 Eval 返回直接提升权限。要求抗页面篡改时，必须补 isolated-world Eval/独立返回通道，当前门槛未满足。
3. 主文档/同源 frame 用 selector 定位；普通 value 或原生 setter + input/change 事件必须经过页面实际状态确认。受控实验只测试 input 事件，不承诺所有控件均需或接受 change。
4. 点击后等待新文档或目标业务状态，再重新提取 DOM；不要保留跨导航 element ID/JS 引用。截图调用 WebView.CapturePage。需要可信输入、跨域 frame 或弹窗的操作返回明确不支持/交由用户。
5. worker 执行带超时的 EvalContext，UI 状态通过 Window.Update 更新。导航/关闭改变代次并取消宿主任务；上下文取消仅终止等待，不能当成页面 JS 一定停止的证据。没有单独实测所有 callback 的先后顺序或压力竞争。

源码核对还发现：`plugins/automation` 的 input 面向 native UI surface，eval 则调用顶层 Window.Page；在 Content 窗口会拒绝并提示使用 tree。不能据此宣称已经获得内嵌 WebView 的可信鼠标/键盘注入。未实测该插件。

## 运行命令、材料与清理

实际执行（从本仓库根目录，使用已有 go.mod 锁定依赖）：

```sh
sw_vers
uname -m
go version
go list -m -f '{{.Dir}}' github.com/egoist/mygo
go build -o "/tmp/mujicagent-issue27.VPmbek/probe" "/tmp/mujicagent-issue27.VPmbek/main.go"
MUJICAGENT_DATA_DIR="/tmp/mujicagent-issue27.VPmbek/data" "/tmp/mujicagent-issue27.VPmbek/probe"
go vet "/tmp/mujicagent-issue27.VPmbek/main.go"
git diff --check
```

为桌面工具识别，将同一二进制复制到临时 `Issue27.app/Contents/MacOS/probe`，创建仅含 executable、identifier、name、package type 的 Info.plist；从终端使用同一 MUJICAGENT_DATA_DIR 运行该二进制。未安装到 Applications。程序同时设置 MyGo PathUserData；网页始终使用临时 Session。

受控网页包括原生表单、依靠 input 监听同步 model 与可见文本的组件、检查 `event.isTrusted` 的按钮、两个源的 iframe 和三种链接。独立 loopback 控制端点只用于调用上述宿主 API；Node 内置 fetch 执行本地请求，没有安装 Node 依赖或将它引入产品。每个异步 Eval 限时 3 秒；导航和布局步骤等候 600–700ms 后读取最终状态。本次探针是探索性验证，非可长期依赖的固定延时自动化测试。

构建、探针 go vet 和 diff 检查通过。仓库仅增加本报告，未运行无关的产品全量测试、打包或真实 Codex 测试。

截图由本次任务交付附件提供，按交付规范不提交截图/临时程序进仓库。原始文件和 SHA256：

| 文件（相对临时目录） | 内容 | SHA256 |
| --- | --- | --- |
| `data/page.png` | CapturePage：受控输入、同源 frame 点击和 JS 可信事件拒绝 | `ae556c605b0caf7572e628c27af3170487bcc0075a476d3ca9d10cc7d2575402` |
| `native-takeover.png` | 缩放后的原生工具栏、网页和接管 accepted 状态 | `519789e787d932ab9be01d8705f2e10379c352e7e043559e9571a5cd7bee2279` |
| `popup.png` | 显式允许后的空白原生弹窗 | `4afa64ccf9594827716b235582e7b16e83061c716323997474e19d41814eff7e` |

临时源码、原始输出 `results.log` / `run.log` 和截图保留在仓库外供本次审阅，不作为第二套示例应用维护。退出后桌面查询意外重新启动过探针，已终止该精确 PID；最后 `pgrep -fl "issue27.*probe"` 无匹配，原测试端口与重启后的网页端口均无 LISTEN。未清理或读取用户浏览器数据，未写产品会话；仓库无临时文件。

## 锁定版本源码索引

- [WebView 创建、销毁与主线程转发](https://github.com/egoist/mygo/blob/v0.7.4/webview.go)
- [原生布局、键盘与鼠标路由](https://github.com/egoist/mygo/blob/v0.7.4/ui/webview.go)
- [Session 生命周期](https://github.com/egoist/mygo/blob/v0.7.4/session.go)
- [PageOptions.Untrusted](https://github.com/egoist/mygo/blob/v0.7.4/page.go)
- [导航、弹窗、截图和 Page API](https://github.com/egoist/mygo/blob/v0.7.4/window.go)
- [Eval 结果封装和取消](https://github.com/egoist/mygo/blob/v0.7.4/bridge.go)
- [macOS Eval 的 pageWorld 实现](https://github.com/egoist/mygo/blob/v0.7.4/internal/darwin/window.go)
- [已有上游 WebView e2e 测试（仅阅读，未运行）](https://github.com/egoist/mygo/blob/v0.7.4/internal/e2e/webview_test.go)
