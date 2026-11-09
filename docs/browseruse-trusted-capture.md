# BrowserUse 可信截图实现

macOS Capture 使用系统 WebKit 的进程内扩展。检查、临时脱敏和原页面视口栅格化在同一次同步调用中完成，图片通过私有原生 IPC 返回。页面不能提供安全结论、回调或位图。其他平台及缺少扩展时返回 Unsupported。

## 事务与返回边界

1. MyGo 的构造钩子配置当前 WebView 的独立 WKProcessPool，加载本应用 bundle；`WithNative` 在主线程借用确切 WebView，不猜测私有结构或遍历视图树。
2. 宿主发送版本、模式、随机 128 位 nonce、固定策略摘要和受限参数。网页没有这个 IPC 入口。
3. WebProcess 的独立 script world 禁用覆盖内建对象行为，并允许检查闭合 shadow root。固定工具脚本及引用观察器也安装在此 world，页面篡改 `JSON.stringify` 或原型不会替换宿主代码。
4. Capture 检查当前 origin、文档、DOM 和样式，暂停自身引用观察器，保存/替换敏感内容。在同一次 JS 调用内通过私有 native callback 进行同步 WebKit snapshot，然后在 `finally` 中恢复值、选区、占位文字和滚动，并恢复引用观察器。
5. 栅格化及 PNG 编码在 WebProcess 中完成。Go 回调验证页面、类型、长度、nonce 和模式后复制私有 WKData；控制器还校验 PNG，再编码为 Codex `inputImage`。不使用页面 `safe=true` 或页面可写的消息桥。
6. 宿主在完成前再次核对页面代次。取消立即撤权，但 Busy 等待实际回应；关闭通过销毁屏障排空，独立超时也销毁页面。导航、取消和失败不返回 PNG。

每次 Observe 仍替换旧引用，页面自身改动仍使引用失效；Capture 的内部脱敏不会清除先前有效引用。原生回归覆盖 Observe→Capture→同引用 Fill。

临时改动会产生页面 MutationObserver 可在恢复后观察到的记录；Capture 不是对任意网页无副作用的承诺。文字替换可能改变排版，不是按猜测矩形遮罩的原图。测试逐像素比较独立脱敏页面，仅说明已覆盖的受限场景。

## 拒绝策略与限额

- 文档必须 complete、origin 精确匹配、字体加载完成且无注册自定义字体。
- 最多 4096 元素、8192 文本节点、262144 文本字符及 262144 样式字符。
- 拒绝 frame、闭合/自定义 shadow root、图片、canvas、SVG/Math、媒体、object/embed、select、自定义元素、动画及无法读取的样式。
- 拒绝资源 URL/paint/image-set、自定义字体/计数器、生成内容和非空列表标记等未纳入凭据检查的渲染来源。
- 输入和 textarea 的当前值与占位文字临时清空；textarea/contenteditable 正文替换。发现常见凭据格式或敏感字段值回显时，全部正文文本替换成 `[redacted]`。
- CSS 视口每边最多 4096，scale 1–4；位图每边最多 8192 且不超过 16777216 像素，PNG 最多 4 MiB；先检查尺寸再栅格化。IPC 与固定工具返回也有界。
- 无法识别没有任何标识的任意秘密；无法安全检查的页面返回 Sensitive，不回传部分图。

## 来源与兼容

实现位于 [capture_darwin.go](../internal/browser/capture_darwin.go) 和 [capturepolicy/native](../internal/browser/capturepolicy/native/)。C ABI 声明对照 WebKit [8e16cbe44f3a7e6aec2a641d612cba2b641cbf87](https://github.com/WebKit/WebKit/tree/8e16cbe44f3a7e6aec2a641d612cba2b641cbf87) 的 InjectedBundle/Shared C API，调用系统 WebKit，没有分发引擎实现。来源及许可见 [第三方声明](../THIRD_PARTY_NOTICES.md)，可重建 MyGo 补丁见 [维护说明](../scripts/README-browser-capture.md)。

helper 为 arm64/x86_64 universal bundle，由 buildCommand 编译、ad hoc 签名并加入平台资源。摘要涵盖 JS 策略、C++、ABI 及固定工具脚本；陈旧 helper 无法接受新事务，宿主超时销毁而不放行图片。当前支持状态表示宿主配置成功，不是跨系统版本的 ABI 握手承诺。

这些是系统 WebKit 私有接口。本机 macOS 27.2 / arm64 有真实运行证据；系统更新、其他 macOS 版本和 amd64 需要对应重验。编译、符号存在或 30 次回归不能证明任意系统版本、任意网页的安全性。Apple 签名/公证发布另行验收。

## 已验证的反例与回归

`TestBrowserNativeCaptureFreeze` 保留了旧方法的确定性反例：先发起 fetch，由服务端扣住响应；禁用 `WKPreferences.javaScriptEnabled` 后，普通 pageWorld Eval 被拒绝，独立 world 仍能读取 DOM；释放响应后 Promise 修改 DOM。该方法不能构成截图屏障，本实现不依赖它。

WebKit 的异步 `takeSnapshotWithConfiguration`、节点句柄或暂停 API 也不能直接与先前独立检查拼成原子截图；相关源码参考：

- [WKWebView.mm](https://github.com/WebKit/WebKit/blob/8e16cbe44f3a7e6aec2a641d612cba2b641cbf87/Source/WebKit/UIProcess/API/Cocoa/WKWebView.mm)：暂停页的异步 JS/snapshot 入口检查。
- [BackForwardCache.cpp](https://github.com/WebKit/WebKit/blob/8e16cbe44f3a7e6aec2a641d612cba2b641cbf87/Source/WebCore/history/BackForwardCache.cpp)：暂停之前仍派发 pagehide。
- [WebFrame.cpp](https://github.com/WebKit/WebKit/blob/8e16cbe44f3a7e6aec2a641d612cba2b641cbf87/Source/WebKit/WebProcess/WebPage/WebFrame.cpp)：后续节点快照解析活节点。

实际回归包括 30 次独立位图对比、10 类复杂内容拒绝、真实原生取消/关页/导航 race，以及最终真实 Codex 六工具与图片传递。命令及验收缺口见 [运行说明](browseruse-runtime.md)。人工焦点、IME 和原生层级仍阻塞 #31/#25 的完整验收。
