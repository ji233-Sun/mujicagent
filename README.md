<h1 align="center">MujicAgent</h1>

<p align="center"><strong>让智能体成为桌面上顺手的工作伙伴。</strong></p>

<p align="center">
  原生界面 · 项目会话 · 图片标注 · 随时引导
</p>

<p align="center">
  <a href="https://github.com/ji233-Sun/mujicagent/releases/latest">下载应用</a> ·
  <a href="docs/usage.md">使用指南</a> ·
  <a href="CHANGELOG.md">更新日志</a> ·
  <a href="https://github.com/ji233-Sun/mujicagent/issues">反馈与建议</a>
</p>

---

MujicAgent 是一个支持 Codex、Pi 和 Droid 的原生桌面客户端。把项目、对话、截图和执行过程放在同一个工作空间里，让你更方便地交代任务、补充想法、查看结果。

它连接本机的智能体 CLI，沿用各自已有的登录和配置。你可以围绕代码项目持续工作，也可以打开一个无项目会话，先把想法聊清楚。

## 为日常协作而设计

### 项目有归属，对话有条理

每个项目都有自己的会话列表，切换任务时可以接着之前的上下文继续。暂时告一段落的对话，手动收纳起来，让侧栏留给正在关注的工作。项目、会话、草稿和收纳状态保存在本机，下次打开可以继续。

### 圈出来，比描述半天更直接

把截图或文件放进消息，在图片上画框、加箭头、写评论，再交给 Codex。调整界面、复现问题、讨论设计时，可以直接指出具体位置。发送标注时会同时提供原图、标注图和评论，让视觉反馈也成为任务上下文。

### 想到下一步，就先写下来

Codex 工作时，你可以继续补充消息。后续任务进入队列，支持编辑、删除和拖动排序；需要调整当前方向时，可以将消息插入正在进行的对话。等待回复的时间，也能用来整理接下来要做的事。

### 看得清过程，也留得住注意力

回复以流式 Markdown 展示，代码块清晰呈现。命令执行、文件修改、思考摘要和工具调用收进可展开的过程记录：平时专注对话，需要时再查看细节。MCP 调用可以展开检查参数、结果和错误。

### 按任务决定怎么协作

选择模型、思考强度和服务端提供的速度档位；为每段对话设置合适的权限。Codex 需要澄清或审批时，直接在对话中作答。运行期间调整的权限从下一轮生效，完全访问需要确认，可用选项遵循 Codex 的服务端策略。

## 原生桌面的日常体验

MujicAgent 使用 Go 和 [MyGo](https://github.com/egoist/mygo) 构建，界面由 MyGo Native UI 绘制。浅色界面、系统毛玻璃侧栏和原生设置面板，让项目与对话各有位置；支持简体中文与英文即时切换，并遵循系统的“减少动态效果”设置。

会话数据保存在本机，登录凭据由 Codex 管理。模型请求仍通过 Codex 使用你配置的服务，具体能力和用量取决于 Codex 账户与配置。

## 开始使用

1. 准备 Codex 登录或 API 配置；也可先打开应用，在设置中一键安装 Codex。
2. 从 [GitHub Releases](https://github.com/ji233-Sun/mujicagent/releases/latest) 下载对应的安装包。
3. 打开 MujicAgent，添加一个本地项目，或从“无项目”开始第一段对话。

| 平台 | 安装包 |
| --- | --- |
| macOS · Apple Silicon | `mujicagent-<版本>-arm64.dmg` |
| macOS · Intel | `mujicagent-<版本>-amd64.dmg` |
| Windows · x64 | `mujicagent-setup-<版本>-amd64.exe` |
| Windows · ARM64 | `mujicagent-setup-<版本>-arm64.exe` |

当前主要运行验证环境为 macOS Apple Silicon。Windows 已提供安装包和 CI 构建检查，尚未完成运行验证；Linux 尚未做运行验证。Windows 安装器暂未配置 Authenticode 签名，可能出现未知发布者或 SmartScreen 提示。

连接配置、附件操作和快捷键见[使用指南](docs/usage.md)。支持本机 Codex、Pi、Droid，以及携带可见对话上下文的跨智能体交接；各自能力边界见使用指南。尚不支持远程连接、worktree 管理或导入 CLI 全局历史。

## 发布与更新

应用会自动检查新版本，也可以在“设置 → 软件更新”中下载更新、查看更新日志并重启安装。安装位置与更新条件见[使用指南](docs/usage.md#安装与更新)。

维护者的构建、签名、公证和发布流程见[发布指南](docs/releasing.md)。

## 参与项目

欢迎通过 [Issues](https://github.com/ji233-Sun/mujicagent/issues) 提交问题和建议。报告问题时，请附上系统、应用与 Codex 版本，以及复现步骤。

想从源码运行或参与开发，请阅读[开发指南](docs/development.md)，了解环境要求、项目结构和验证命令。

## 致谢

- [Codex](https://github.com/openai/codex)：提供本地 Agent 能力与 App Server。
- [MyGo](https://github.com/egoist/mygo)：提供原生 UI、桌面能力与打包工具。
- [T3 Code](https://github.com/pingdotgg/t3code)：启发了项目与会话组织、手动收纳和 Agent 管理的交互。
- [Lucide](https://lucide.dev/)：提供界面图标。

第三方来源与许可见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
