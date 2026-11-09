# BrowserUse 受限实现与验证

本分支实现 #29 的执行器，并将 #31 的原生面板与 Codex 接到同一 Runtime。用户已选择受限首版，随后明确要求补足 MyGo 可信截图边界；授权、隔离、脱敏和取消要求继续有效。PR #105 的原始四文件差异已逐文件审查，未发现需修复的问题。

macOS 现在提供受限的安全截图路径，真实 Codex 六工具链路已通过。#31/#25 仍有人工焦点、IME、原生层级等验收缺口，不能据此声明总任务全部完成。

## 实现与使用

`main.go` 将同一个 `browser.Runtime` 交给 Controller 和 BrowserPanel。每个本地 ThreadID 对应独立临时 Session 和活动 PageKey。新页默认 Human，权限关闭；显式恢复后才允许 Agent Observe，Navigate、Interact、Capture 分别授权。模型不能指定 Actor、ThreadID、selector、任意 JS 或原生句柄。

跨源顶层导航只由人工地址栏授权。历史导航即使绕过 `OnWillNavigate`，也会在原生提交或 URL 刷新时撤权。外部页面保持 `Untrusted: true`，不开放 Go 绑定，默认拒绝弹窗、下载和权限请求。只提供主文档 DOM 操作，iframe、上传/下载和可信事件交由人工处理。

操作按页互斥，不排队。宿主生成随机页面、观察和元素标识；下一次观察、写入、实际 DOM 变化、导航或接管使旧引用失效。Capture 内部的临时脱敏不使既有引用失效；网页自身之后发生的变化仍会失效引用。

取消立即撤权并结束调用者等待，但 Busy 保持到原生事务排空。无法确认排空时按独立的 15 秒原生截止时间销毁页面。关页是终态，重建取得新 PageID 和空 Session；迟到完成不能恢复权限。已提交的页面或网络副作用不能撤回。

Codex 动态工具只在有浏览器页面的新线程中注册；同连接已注册线程可以续用。旧线程或重连后无法确认注册时明确失败并保留历史。工具上下文由宿主按轮次绑定，不能用当前选中的对话替代路由。其他 Harness 不注入浏览器工具。

## 能力与边界

| 能力 | 当前行为 |
| --- | --- |
| Observe | 有界可见文本与支持控件，不返回 HTML、storage、Cookie、headers 或 input value |
| Click / Fill | 普通 DOM 事件，核对可观察变化及输入消费；无法确认返回 unconfirmed，不承诺业务成功 |
| Scroll | 主视口 CSS 像素；实际偏移未变化返回 unconfirmed |
| Capture | macOS 可信扩展在同一 WebProcess 调用中检查、暂时脱敏、同步栅格化并恢复；复杂内容拒绝 |
| 不具备扩展的平台/安装 | Capture unsupported，权限项禁用并解释原因；Windows/Linux 未提供该扩展 |
| 结果通道 | 支持扩展的 macOS 使用独立 world 和私有原生 IPC 执行固定工具；其他平台保留不可信 pageWorld 路径 |
| Session | 每对话临时隔离，不导入系统浏览器数据，不跨重启保留网页登录 |

截图隐藏所有输入值及占位文字；发现已知凭据或敏感字段值回显时，保守替换全部正文文字。frame、闭合 shadow DOM、图片、绘图、媒体、自定义元素、动画、生成内容、不可读取样式或视觉资源等无法可靠检查的内容返回 Sensitive，不返回部分 PNG。完整策略、大小限制、源码依据及兼容风险见[可信截图实现](browseruse-trusted-capture.md)。

识别规则不能保证发现没有任何标识的任意秘密。截图需要独立授权；工具活动只保存名称、状态、分类、计数和图片尺寸，不持久化页面正文、输入值或 PNG。网页仍是不可信内容，不能提供宿主授权或身份。

## 构建

MyGo 仍锁定 v0.7.4，使用标准 `vendor/`，加入三个文件的原生互操作补丁。没有替换浏览器引擎或增加运行服务。补丁来源、重建与许可见 [vendor 维护说明](../scripts/README-browser-capture.md)。

```sh
# macOS，需 Xcode Command Line Tools；生成 universal 原生扩展
go run ./scripts/build-browser-capture
# MyGo 打包自动执行上述 buildCommand，并复制 platform resources
go tool mygo build -platform "darwin/arm64,darwin/amd64"
```

直接 `go run .` 或原生测试前先构建扩展；否则截图明确不支持。生成 bundle 不提交。Go 仍可 `CGO_ENABLED=0` 构建。Windows 构建脚本不执行原生编译。

## 可重复验证

普通检查不启动原生网页或模型：

```sh
CGO_ENABLED=0 go test ./... -count=1
go test -race ./internal/harness ./internal/codex ./internal/controller ./internal/browser -count=1
go tool mygo vet .
go vet ./...
go tool mygo i18n check
```

真实 WebView 验收只访问随机 loopback 合成页，直接设置测试状态，不模拟键鼠：

```sh
go run ./scripts/build-browser-capture
MUJICAGENT_BROWSER_NATIVE_TEST=1 \
go test ./internal/view -run '^TestBrowserNative(Runtime|ResumeThenSwitch|CaptureFreeze|TrustedCapture|CaptureRejectsOpaqueContent)$' -v -count=1 -timeout 90s

MUJICAGENT_BROWSER_BRIDGE_TEST=1 \
go test -race ./internal/browser -run '^TestNativeCaptureDrain$' -v -count=1 -timeout 100s
```

真实 Codex 测试会使用本机登录并产生模型用量，必须另获授权。本任务已有明确授权，仅操作隔离合成页：

```sh
MUJICAGENT_BROWSER_NATIVE_TEST=1 MUJICAGENT_BROWSER_LIVE_TEST=1 \
MUJICAGENT_BROWSER_LIVE_MODEL=gpt-6.1-sol \
go test ./internal/view -run '^TestBrowserNativeCodex$' -v -count=1 -timeout 5m
```

CPU 布局截图由 `ui.NewTester` 直接设置场景：

```sh
MUJICAGENT_BROWSER_SCREENSHOTS="$PWD/build/browseruse-acceptance/cpu" \
CGO_ENABLED=0 go test ./internal/view -run '^TestBrowserLayoutScreenshots$' -count=1
```

## 本次记录与剩余项

环境为 macOS 27.2 / arm64、Go 1.27.3、MyGo v0.7.4、Codex CLI 0.167.0。

- 原生受控输入、实际表单导航和滚动、引用失效、Observe→Capture→同引用 Fill、双临时 Session 同源存储隔离、关页重建、Go 绑定拒绝、历史跨源撤权和恢复后切换撤权通过。
- 30 次产品 Runtime 截图与独立脱敏原生位图逐像素对比通过，包含 body 外可见凭据、短密码、Basic、序列化/原型篡改、timer、Promise、MutationObserver 与 ResizeObserver；13 类不透明/复杂内容拒绝并保持 PNG 为空。旧策略对 body 外文字及可显示 script/style/head 的失败先原生复现，再以相同场景确认修复。
- 原生截图中取消、关页、导航三个场景在 race 下通过：取消后保留 Busy，关页销毁屏障，导航返回 stale，均无图片泄漏。
- 最终策略从已打包 Resources 加载，真实 Codex 六工具验收通过，56.23 秒、12 条活动；模型收到实际原生截图，页面提交值和滚动结果吻合。包含该轮次的完整原生组合共 60.282 秒通过。
- 全量 `CGO_ENABLED=0 go test ./...`、四包 race 已通过。初次全量检查受本地原型目录干扰并超时，隔离后完整重跑通过；首次静态检查发现 native pointer 声明告警，修正为原生指针类型。
- 修正后 MyGo vet、Go vet、i18n、生成检查通过。最终 macOS 双架构打包 13.7 秒，两份 `.app` 的 deep/strict 签名检查通过，包内含 universal helper 和许可声明。bridge 夹具采用响应门控，确认阻塞请求仍实际在途后才中断，最终包内扩展 race 通过（3.165 秒）。最终策略还重跑 browser/view 全包与 MyGo/Go vet；工具摘要换行已有本次 CPU 截图。Windows amd64/arm64 的 Go 交叉构建通过。
- CPU 共 40 张：中英文、840×560/1440×900，涵盖人工/Agent/加载/排空/错误/收起/权限/截图不可用/未连接/工具记录。
- CI `42599076745`（2d2d7b2）macOS verify/双架构打包及 Windows 检查/双架构安装器均通过。历史 `42583794768` 的 Windows HTTP 504 不作为通过记录；最终 document 文本范围补强仍以其后提交的 CI 为准。
- 尚未完成人工焦点、IME、原生窗口层级、完整停止/删除/断线重连与应用重启流程；Windows/Linux 原生运行及 GPU 未验证。系统 WebKit 私有接口须按系统版本重验，跨架构构建不能替代运行证据。
