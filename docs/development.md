# 开发指南

[返回产品介绍](../README.md) · [使用指南](usage.md) · [发布指南](releasing.md)

以下命令均在仓库根目录执行。

## 开发

需要 Go 1.27.3+，以及已安装并登录的 [Codex CLI](https://developers.openai.com/codex/cli)。

```sh
codex login
go tool mygo dev
```

Codex 连接配置见[使用指南](usage.md#连接-codex)。

```sh
go test -race ./...
go vet ./...
go tool mygo vet
go tool mygo i18n check
go tool mygo build
```

macOS 构建输出位于 `build/darwin-arm64/`，包含 `.app` 和 `.dmg`。本地和普通 CI 构建使用 ad-hoc 签名。Release 工作流使用 Developer ID 签名和 Apple 公证，更新包另外使用独立的 Ed25519 签名校验。已发布的 `v0.1.1` 使用 ad-hoc 签名，自动公证从后续发版开始启用。

Windows 使用 `go tool mygo build -platform "windows/amd64,windows/arm64"`，输出位于 `build/windows-<架构>/`，安装器为 `mujicagent-setup-<版本>-<架构>.exe`。Windows 上 MyGo 自动下载所需的 NSIS；在其他系统交叉构建安装器需预先安装 NSIS。普通 CI 在 Windows x64 runner 上运行应用测试和静态检查，并构建两种架构的安装器，上传 `windows-preview`；macOS 发布脚本的 Shell 集成测试仍在 macOS CI 执行。Windows ARM64 当前只验证构建。

可选的真实连接测试会使用当前 Codex 登录，发送简短的测试消息并产生模型用量；不要求读取或修改项目文件：

```sh
MUJICAGENT_LIVE_TEST=1 go test ./internal/controller -run 'TestLiveCodex|TestLiveAsk' -v -timeout 4m
```

## 架构

保持简单的 MVC，只有 MyGo 和它自带的依赖，没有额外前端框架、数据库或服务：

```text
main.go                        应用和原生窗口入口
internal/model/state.go        项目、会话、配置与原子文件保存
internal/controller/           用户操作、会话生命周期、事件归并
internal/harness/              统一驱动目录、会话契约、CLI 协议与安装管理
internal/view/                 原生视图、输入状态、交互与确认框
internal/codex/                stdio JSON-RPC 传输与协议类型
locales/                       MyGo 中英文语言目录及生成的类型安全访问器
```

后台操作通过事件队列回到 UI 线程修改状态；视图不直接操作子进程。Codex、Pi、Droid 共用驱动注册、发送与队列确认、事件归并和结束处理，各驱动保留自己的原生协议。分层、生命周期、能力边界及新增 Harness 的验收要求见 [Harness 驱动架构](harness-architecture.md)。Codex App Server 按 `initialize → initialized → thread/start 或 thread/resume → turn/start` 工作，协议以 [官方文档](https://developers.openai.com/codex/app-server) 和本机 CLI 生成的 JSON Schema 校验。

修改 `locales/zh-Hans.json`、`locales/en.json` 后运行 `go tool mygo generate`；开发和构建命令也会生成访问器。语言使用稳定标识 `zh-Hans`／`en` 保存，旧数据默认使用中文。

数据位于 `os.UserConfigDir()/MujicAgent`，macOS 为 `~/Library/Application Support/MujicAgent`。`state.json` 使用临时文件加原子替换保存；新建无项目会话在首次发送时分配 `~/.mujicagent/workspace/YYYY/MM/<slug>-<id8>/` 独立工作目录，空白草稿不创建目录；年月取会话创建时间，短名只含 ASCII 英文、数字和连字符（最长 24 字符，无英文时使用 `chat`），随机后缀保证排他创建。路径保存后不随标题或时间变化，已有会话目录不迁移。`MUJICAGENT_DATA_DIR` 可指定隔离的数据目录，此时新会话工作目录也使用该目录下的 `workspace/`。凭据由 Codex 管理，应用不复制凭据文件。
