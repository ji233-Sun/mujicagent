# 开发工作流

本文件适用于整个仓库；用户本次明确要求优先于默认约定。

## 输出约定

- 禁止描述性文本，除非明确要求。

## 强制遵循的交付技能

- 任何变更任务（包括代码、配置、测试、文档、AGENTS.md 和 Skills）开始时，**修改前必须读取并遵循 [mujicagent-delivery](.agents/skills/mujicagent-delivery/SKILL.md)**。提交、推送、创建或更新 PR 及关联 Issue 时同样适用。
- 这是仓库强制约束，不是可选建议。即使客户端未自动发现该技能，也必须直接读取上述路径，不能因技能列表中未显示而跳过。
- 所有变更必须在独立工作分支完成，验证后提交、推送，并向 GitHub 主分支创建或更新 PR，交付提供链接。变更请求已授权该流程，无需逐步等待确认；创建 PR 不代表授权合并或发布版本。
- 新功能使用 `feat/`，修复使用 `fix/`，文档等使用对应的有意义前缀；禁止 `codex/`。不得直接在主分支提交或推送，不得丢弃或混入无关的未提交工作。
- 分支检查、验证选择、中文 PR、Issue 关闭关联和发布边界统一维护在交付技能中，不在此重复细则。

## UI 验收

- UI 布局与视觉验收使用纯 CPU 离屏测试和本次生成的 PNG：直接设置场景状态，再渲染截图；不使用 Computer Use，也不通过模拟点击、键盘或鼠标操作切换截图场景。
- 具体流程和能力边界遵循 [mygo-maintenance](.agents/skills/mygo-maintenance/SKILL.md#verification)，截图交付遵循 [mujicagent-delivery](.agents/skills/mujicagent-delivery/SKILL.md#最小充分验证)。CPU 截图不作为 GPU 或系统原生窗口行为的验证证据。

## Harness 扩展

- 修改智能体设置、harness 安装更新、认证或新增 agent 时，读取并遵循 [harness-maintenance](.agents/skills/harness-maintenance/SKILL.md)，特别注意 Windows 原生程序与 npm 启动器、安装来源和非账号认证的区别。
