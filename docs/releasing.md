# 发布指南

[返回产品介绍](../README.md) · [开发指南](development.md)

面向维护者。安装和应用内更新见[使用指南](usage.md#安装与更新)。

## 发布流程

[Release 工作流](https://github.com/ji233-Sun/mujicagent/actions/workflows/release.yml) 仅支持手动触发，有尚未正式发布的提交才构建。已发布的提交及更旧的排队运行直接跳过。取消定时发布的配置合入默认分支后生效。

手动发布步骤：

1. 选择 **Run workflow → main**。
2. 选择 `patch`、`minor` 或 `major`，分别递增修订号、次版本或主版本；低位版本号自动清零。手动正式发布同样要求有新提交。
3. 正式发版保留 `dry_run=false`。试运行勾选 `dry_run`：执行相同的校验、macOS 和 Windows 双架构构建、更新签名及 Apple 公证，只上传 Actions 构建产物，不修改版本、标签或 Release。试运行也允许选择开发分支，用于验证工作流改动。

稳定版 `vX.Y.Z` 标签是发布版本号的唯一依据：按数值取最高标签并递增；没有标签时才以仓库 `mygo.json` 的版本为起点。已创建但未发布成功的标签也占用版本号，避免复用。失败后新运行可能跳过一个版本号，这是有意保留的发布记录。

工作流固定使用触发时的源码提交，仅在构建目录注入 `mygo.json` 版本及 `CHANGELOG.md` 发布说明，所有平台复用同一份版本配置。测试、公证和最终产物验证通过后，直接在该源码提交上创建带运行 ID 的 `vX.Y.Z` 标签；全部资产上传完成后才公开 Release。流程不创建版本 commit，也不推送 main；构建期间 main 前进不影响当前发布，新提交由下次手动发布处理。仓库内的版本及历史日志不再自动回写，正式版本与完整发布记录以 GitHub Releases 为准；源码本地构建默认仍使用仓库中的版本值。

普通 push 和 PR 运行 [CI](https://github.com/ji233-Sun/mujicagent/actions/workflows/ci.yml)，不会自动发布。所有 Release 运行共用并发锁，不取消正在签名或公证的运行。发布前检查源码仍属于 main；若出现更高版本标签，旧检查点停止发布，防止 latest 回退。

发布分为 `check → build → submit → finalize → publish`，中间产物保留 30 天：

Windows 任务在 `build` 后与 Apple 公证并行，在 Linux runner 上使用 NSIS 交叉构建 amd64 和 arm64 安装器。它复用 `release-build` 中固定版本的配置和日志，生成两个架构的安装器、Ed25519 签名更新归档和 `update-windows-<架构>.json`，保存为带 SHA256 清单的 `release-final-windows` 检查点。重试优先复用并校验该检查点；Windows 构建或任一 macOS 公证失败都会阻止发布。所有平台的资产上传完成后才公开同一个 Release。

- `release-build` 保存固定版本的签名产物、源码提交、发布说明与 SHA256 清单。
- `release-submission-arm64/amd64` 分别保存 Apple submission ID、架构、版本和原始 DMG SHA256；提交完成即保存，不等待公证结果。
- `release-final-arm64/amd64` 保存装订后验证通过的 DMG、更新包、更新清单与 SHA256 清单。
- `notary-logs-*` 保存每次等待的状态和可获取的 Apple 公证日志。

网络失败或等待超时后，在**原运行**选择 **Re-run failed jobs**，或仅重跑对应任务。已保存的构建和 submission ID 会被复用，不会重新编译或重新提交公证。**不要选择 Re-run all jobs**：GitHub 会清除上一轮 artifacts；脚本发现重试缺少构建检查点会停止，避免悄悄重新构建。若首次构建失败、尚未保存检查点，或检查点已丢失，需要新建 **Run workflow** 明确重新构建。`finalize` 等待同一 ID 最多 20 分钟，只有 `Accepted` 才装订；`staple` 和 `validate` 的临时网络错误最多尝试 5 次，间隔 10、20、40、60 秒。`Invalid/Rejected` 必须检查 Apple 日志并修复代码，重复等待不能修复拒绝原因。

检查点过期或缺失时不能保证恢复。特别是 Apple 已接受上传、但客户端未收到 ID，或 ID 尚未成功上传为 Actions 产物时，需先检查 Apple 提交历史；重试可能再次提交。新建 **Run workflow** 是全新发布，不读取旧运行的检查点，可能重新构建和公证。

### Apple notarization

在[仓库 Actions Secrets](https://github.com/ji233-Sun/mujicagent/settings/secrets/actions) 中配置以下五项：

| Secret | 内容 |
| --- | --- |
| `MACOS_CERTIFICATE_P12` | 包含私钥的 **Developer ID Application** `.p12` 文件的 Base64 编码 |
| `MACOS_CERTIFICATE_PASSWORD` | `.p12` 导出密码 |
| `APPLE_API_KEY_ID` | App Store Connect 团队 API Key ID |
| `APPLE_API_ISSUER_ID` | 团队 API Issuer ID |
| `APPLE_API_PRIVATE_KEY` | `.p8` 文件的完整原文，保留 BEGIN/END 行与换行 |

证书必须是用于 Mac App Store 之外分发的 **Developer ID Application**，不能使用 Apple Development、Apple Distribution 或 Developer ID Installer。证书需要同时导出私钥，`.p12` 中只放一个有效的 Developer ID Application 身份。证书与 API 密钥应属于同一开发者团队。参见 [Apple 证书说明](https://developer.apple.com/help/account/certificates/create-developer-id-certificates/)和[团队 API 密钥说明](https://developer.apple.com/help/app-store-connect/get-started/app-store-connect-api/)。

可以在本地直接上传文件内容，避免把私钥打印到终端或写进仓库：

```sh
base64 -i "/path/to/DeveloperID.p12" | gh secret set MACOS_CERTIFICATE_P12 --repo ji233-Sun/mujicagent
gh secret set MACOS_CERTIFICATE_PASSWORD --repo ji233-Sun/mujicagent
gh secret set APPLE_API_KEY_ID --repo ji233-Sun/mujicagent
gh secret set APPLE_API_ISSUER_ID --repo ji233-Sun/mujicagent
gh secret set APPLE_API_PRIVATE_KEY --repo ji233-Sun/mujicagent < "/path/to/AuthKey.p8"
```

构建阶段创建临时 Keychain 导入证书，MyGo 对两个架构的应用和 DMG 签名，以 `-skip-notarize` 保存待公证产物。提交和等待阶段使用临时 API 私钥文件。公证 Accepted 后，将票据附加到 DMG 和用于自动更新的应用，再重新打包并生成对应最终归档的 Ed25519/SHA256 签名；保留 MyGo 更新清单的版本、URL、说明和日期，当前要求 `updates.deltas=0`。发布前会检查代码签名、DMG 的公证票据，并解压自动更新包验证其中应用的票据。任何一步失败都不发布；凭据缺失时也不会降级为 ad-hoc 发布。任务结束时清理临时 Keychain 和私钥文件。

签名配置只在 CI 构建期间注入，不写回仓库。首次配置完成后，建议运行一次 `dry_run=true`，确认签名、公证、票据与更新归档检查全部通过，再正式发布。

仓库 Actions Secret `MYGO_UPDATER_PRIVATE_KEY` 存储更新签名私钥的**内容**。公钥固定在 `mygo.json`；私钥不进仓库、不进入应用。维护者须安全备份私钥，丢失或随意替换会让旧客户端无法验证后续更新。只有签名构建会生成 `update-darwin-*.json`／`update-windows-*.json` 和对应 `.tar.gz`；各平台更新清单、归档和安装包必须一起保留在 Release 中。此签名不能替代 Apple Developer ID 公证或 Windows Authenticode 签名。

若上传失败留下草稿，在更高版本标签出现前重跑原运行的失败任务即可复用 `release-final-*` 补齐资产。发布脚本只接受源码提交及运行 ID 完全匹配的已有标签，仅补传草稿；若该版本已正式发布则直接结束，不覆盖已发布资产。
