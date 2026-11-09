# MyGo 原生互操作补丁与截图构建

Go 依赖仍锁定 `github.com/egoist/mygo v0.7.4`，对应 upstream commit `9bd34e1eb9525d9a1fe698aadc964c49c8ff8e7e`。采用标准 `vendor/`，不依赖本机 clone、`replace` 或 `go.work`。所有原始许可证随依赖保留。

只有三个 MyGo 文件相对标准 vendor 有改动：

- `webview.go`：macOS 配置回调 `ConfigureWKWebView` 与主线程借用 `WebView.WithNative`。其他系统不接受 macOS 配置回调。
- `internal/platform/platform.go`：转发构造配置。
- `internal/darwin/window.go`：在创建确切 WKWebView 之前调用配置回调。

这些钩子默认不生效，不修改普通 WebView 行为，不向网页开放 native handle。应用的扩展/C++ 在 `internal/browser/capturepolicy/native/`，没有放入 MyGo 包，不使用 cgo。

## 重建 vendor

在独立干净工作副本中运行；以下命令会替换 vendor，不要对有其他 vendor 修改的目录直接执行：

```sh
go mod vendor
git apply --directory=vendor/github.com/egoist/mygo scripts/mygo-native-capture.patch
git diff --check
```

补丁为相对 MyGo 仓库根目录的标准 Git diff。更新 MyGo 或重新 vendor 后必须重放、检查并运行本项目原生测试，不能无提示丢弃钩子。补丁应与 vendor 三文件保持一致；其余 vendor 文件应逐字节等于标准 `go mod vendor` 输出。

维护阶段还在独立上游副本执行过 `CGO_ENABLED=0 go test . -run WebView -count=1`。未启用钩子的样例二进制增量为 counter-native 16 字节、hello 80 字节，这仅是对应版本和本机构建的比较，不代表所有程序大小。

## 扩展构建与打包

macOS 需要 Xcode Command Line Tools：

```sh
go run ./scripts/build-browser-capture
go tool mygo build -platform "darwin/arm64,darwin/amd64"
```

`mygo.json` 的 buildCommand 自动执行脚本。它构建 universal bundle，放在 `resources/darwin/MujicAgentCapture.bundle/`，由 MyGo 复制进 `.app/Contents/Resources/`。bundle、临时头文件与二进制不提交。非 macOS 主机脚本直接返回，当前不支持从其他系统构建 macOS helper。

开发时直接 `go run .` 或开启原生测试前先运行脚本。缺少 bundle、符号或配置能力时 Capture 不可用；helper 与宿主策略摘要不匹配时事务失败并按截止时间销毁页面。不要把普通 `CapturePage` 当作可信 Capture 的降级路径。

helper 使用系统 WebKit 私有 C ABI。每个实际支持的 macOS/架构需要原生运行验证；产物构建成功不能替代。签名、公证和发行不是本次本地打包检查的结果，按项目发布流程另行授权执行。

安全边界、限制和测试入口见 [可信截图实现](../docs/browseruse-trusted-capture.md)。
