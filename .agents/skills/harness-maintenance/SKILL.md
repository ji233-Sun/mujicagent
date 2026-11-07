---
name: harness-maintenance
description: 扩展或维护本仓库智能体设置、harness 安装更新和认证状态时使用，包含 Windows 与大陆镜像兼容约束。
---

# Harness 管理

先读 `internal/harness`、`internal/controller/harness.go`、`internal/codex/executable.go` 和 `internal/view/agents.go`。参考 [T3 Code](https://github.com/pingdotgg/t3code) 的 provider 独立身份、设置分组、托管与外部安装分离；不复制其框架或固定版本。

- 当前仅 Codex。新增 harness 时以稳定 driver ID 关联卡片和独立状态；把安装与更新能力和会话协议适配分开。当前 Drivers 是目录，不是已经支持任意协议的注册系统。第二个真实 harness 加入时将 Controller 的单一 Harness 状态改为按 ID 管理，并注入对应的探测/安装实现；不要复用 Codex 的路径、账户、版本或连接状态。
- 保留已有 CodexPath 与会话格式；新增设置使用向后兼容的默认值。自动检查默认每天，只查版本，不安装。手动操作显示进度、错误、取消及可重试入口；所有状态通过 Controller 队列发布，禁止在渲染中启动网络或进程。
- 探测必须沿用连接使用的桌面 PATH 解析，并尊重显式路径。只有证据明确时才能用 npm/Homebrew 原地更新；未知安装只能明确提供“安装并切换到托管版”，不覆盖原文件。任务运行或连接中禁止更新。
- 官方渠道采用完整原生发行包，不能只复制 codex 可执行文件：伴随 host、rg 等也需要保留。大陆选择 npm 镜像，在私有 prefix 安装；无 npm 或 Windows 使用镜像平台包，提供免 Node 选择。不要全局安装 Node、修改 PATH/npm 配置或使用 sudo。
- 先验证 HTTPS 下载摘要、解包路径、版本和 app-server 可用性，再切换配置路径。托管安装使用新目录，失败只清理本次临时目录；旧目录保留供原有进程使用。不要在更新过程中清理用户自定义目录。
- Windows 单独检查 amd64/arm64、空格/中文路径、PATHEXT、npm .cmd 与 .exe。已知 npm 布局直接解析原生 exe；npm 更新经 node + npm-cli.js 参数数组执行。不要把用户路径插入 cmd.exe/PowerShell 命令字符串；未知 wrapper 要求显式 exe。更新前释放客户端句柄，新安装使用独立版本目录，避免覆盖运行中的文件。
- 认证由 harness 管理。Codex 的 account:null 不等于未登录：检查 requiresOpenaiAuth；API key、第三方 base_url 与 env_key 配置由原有 Codex 配置和进程环境继承，不复制密钥到应用状态、日志或 UI。配置状态不代表凭据已验证。
- account/read 仅对 -32603 且 workspace routing discovery timed out 做最多三次、500ms/1s 退避；独立请求预算、可取消。不重试认证错误，不重放 turn/start、登录或其他写请求。

验证扩展现有测试：缺失/外部/托管安装、安装失败不切换、下载摘要及路径、版本比较、取消、账户模式、重试上限、设置展开和切页。按交付技能运行 race、MyGo vet、i18n、原生 UI 验收及 Windows CI。原生包下载测试通过 `MUJICAGENT_INSTALL_TEST=official` 或 `cn` 显式开启，只写临时目录；不调用真实模型，不作为每次默认测试。跨平台编译不能替代 Windows 原生执行证据。
