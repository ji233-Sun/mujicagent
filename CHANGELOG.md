# Changelog

## 0.1.7

#### What's Changed
* fix: 统一更新清单来源并修正检查状态 by @ji233-Sun in https://github.com/ji233-Sun/mujicagent/pull/12
* fix: 删除设置页冗余说明文字 by @ji233-Sun in https://github.com/ji233-Sun/mujicagent/pull/15
* fix: 支持 MCP elicitation 并统一审批卡片样式 by @ji233-Sun in https://github.com/ji233-Sun/mujicagent/pull/18
* feat: 支持会话权限调整并显示生效边界 by @ji233-Sun in https://github.com/ji233-Sun/mujicagent/pull/20
* fix: 仅在权限调整待生效时显示简短提示 by @ji233-Sun in https://github.com/ji233-Sun/mujicagent/pull/21
* fix: 让对话流自适应宽屏 by @ji233-Sun in https://github.com/ji233-Sun/mujicagent/pull/22
* feat: 支持消息排队、拖动排序与对话引导 by @ji233-Sun in https://github.com/ji233-Sun/mujicagent/pull/23


**Full Changelog**: https://github.com/ji233-Sun/mujicagent/compare/v0.1.6...v0.1.7

### Commits

- fix: 移除队列和插入消息的说明标签 (5bbebf2)
- feat: 支持消息排队、拖动排序与对话引导 (4804783)
- fix: 让对话流自适应宽屏 (5b550ba)
- fix: 仅在权限调整待生效时显示提示 (e4d34e2)
- fix: 精简权限生效提示文案 (79921d5)
- feat: 支持会话权限调整并显示生效边界 (f8b52fe)
- fix: 支持 MCP elicitation 并统一审批卡片样式 (a574453)
- fix: 删除设置页冗余说明文字 (da60ee5)
- fix: 统一更新清单来源并修正检查状态 (7d5428b)

## 0.1.6

#### What's Changed
* fix: 附件连续编辑、直接粘贴与图片气泡标注 by @ji233-Sun in https://github.com/ji233-Sun/mujicagent/pull/14


**Full Changelog**: https://github.com/ji233-Sun/mujicagent/compare/v0.1.5...v0.1.6

### Commits

- fix: 改为连续编辑的行内附件并支持粘贴和气泡标注 (56bb7f7)

## 0.1.5

#### What's Changed
* feat: 支持行内附件与图片坐标标记 by @ji233-Sun in https://github.com/ji233-Sun/mujicagent/pull/8
* docs: 禁止未明确要求的描述性文本 by @ji233-Sun in https://github.com/ji233-Sun/mujicagent/pull/9
* feat: 支持可恢复的自动公证与发布流程 by @ji233-Sun in https://github.com/ji233-Sun/mujicagent/pull/10
* fix: 阻止检查点丢失时重复构建与公证 by @ji233-Sun in https://github.com/ji233-Sun/mujicagent/pull/11


**Full Changelog**: https://github.com/ji233-Sun/mujicagent/compare/v0.1.4...v0.1.5

### Commits

- fix: reject rebuilds when full reruns discard checkpoints (6f5e51b)
- fix: allow time for notarization polling and ticket retries (0f2ac39)
- feat: make notarization resumable across release retries (7dfc52e)
- docs: 禁止未明确要求的描述性文本 (58fd5f1)
- feat: support inline attachments and image annotations (898dffb)

## 0.1.4

#### What's Changed
* feat: 支持 Codex 默认权限配置 by @ji233-Sun in https://github.com/ji233-Sun/mujicagent/pull/1
* docs: 强制交付流程并简化 Issue 管理 by @ji233-Sun in https://github.com/ji233-Sun/mujicagent/pull/2
* feat: 添加开发者日志和浮动错误提示 by @ji233-Sun in https://github.com/ji233-Sun/mujicagent/pull/5
* feat: 移除全局新建入口并要求会话关联项目 by @ji233-Sun in https://github.com/ji233-Sun/mujicagent/pull/6

#### New Contributors
* @ji233-Sun made their first contribution in https://github.com/ji233-Sun/mujicagent/pull/1

**Full Changelog**: https://github.com/ji233-Sun/mujicagent/compare/v0.1.3...v0.1.4

### Commits

- feat: require a project to start sessions (11b6640)
- feat: 添加开发者日志和浮动错误提示 (56cc6ef)
- docs: 简化 Issue 分类与管理约定 (3d10358)
- docs: 添加并强制遵循交付技能 (9cc60c8)
- feat: configure default Codex permissions (0aad554)

## 0.1.3

**Full Changelog**: https://github.com/ji233-Sun/mujicagent/compare/v0.1.2...v0.1.3

### Commits

- ci: retry each macOS architecture independently (1c422dd)
- ci: retry interrupted Apple ticket downloads (c63244d)
- fix: discover Codex from the macOS login shell (495cfb7)

## 0.1.2

**Full Changelog**: https://github.com/ji233-Sun/mujicagent/compare/v0.1.1...v0.1.2

### Commits

- ci: notarize Developer ID signed macOS releases (aab4c1a)

## 0.1.1

**Full Changelog**: https://github.com/ji233-Sun/mujicagent/commits/v0.1.1

### Commits

- fix: keep native appearance light across the application (030d519)
- feat: initialize native Codex client with signed app updates and release workflows (d30875f)

## 0.1.0

- 全原生 Codex 桌面客户端，支持项目、无项目对话、手动收纳与中英文界面。
- 流式回复、执行过程分组、Ask 应答与多档权限控制。
- 模型、思考强度与速度选择，原生毛玻璃及轻量动效。
- GitHub Release 更新检查、签名验证、安装更新与历史更新日志。
