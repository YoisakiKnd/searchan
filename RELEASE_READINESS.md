# 发布候选验证记录

2026-10-10，macOS arm64，Go 1.27.2，Typst 0.15.1，govulncheck 1.8.0。状态：用户已开始 Linux 部署联调，完整生产验收尚未完成。

AI 总结优先行为已随 [v0.1.0-rc.2](https://github.com/YoisakiKnd/searchan/releases/tag/v0.1.0-rc.2) 发布：仅有搜索摘要或独立核验遇到问题时生成一次带限制说明的简短总结；缺少模型、生成失败、格式或引用不合法则报错，不再返回摘录。RC.1 保留旧行为，不覆盖已有版本。

当前主分支新增 OpenAI Chat Completions / Responses、Anthropic Messages 三种模型协议、自定义端点与认证、显式思考配置和有预算的一次临时传输重试。独立模型连接池移除了搜索客户端的 15 秒响应头限制，完整请求仍有 deadline。新增 `model check` 以相同配置检查约 10 KB 合成输入与 JSON 输出。用户提供的连接重置日志不能单独确定故障根因；尚未使用用户的实际凭据验证这次修复。

以上变更已通过本地完整 `scripts/verify.sh`：三种协议均验证首次连接中断、一次恢复、引用校验、实际 Typst PNG 和模拟平台交付；另覆盖截断 / 拒答 / 无效 JSON、认证错误和 429 不重试、取消、重试次数及时间预算。模型逻辑调用最多 5 次，默认含重试最多 10 次 HTTP 请求。全项目 race、vet、漏洞扫描、离线回归及中文 PNG 通过，无新增 Go 依赖。

## 已执行并通过

- `scripts/verify.sh`：全项目 race / coverage、go vet、govulncheck、编译、10 题离线回归、真实中文 PNG。扫描结果：No vulnerabilities found。
- Telegram 本地端到端：Webhook 鉴权及重复事件、实际 Typst 编译、PNG multipart 上传到模拟平台、优雅停机。未发送真实 Telegram 消息。
- 安装器暂存：首次安装、相同版本重复安装、不同内容版本保护、升级保留配置和数据、回滚、切换失败恢复旧激活及配置、环境文件不执行且不打印凭据。
- Typst 本地归档：错误 SHA-256 拒绝且不激活，正确摘要提取预期程序；这些检查在临时目录执行。
- Linux amd64 / arm64 静态二进制和发布包构建；两个压缩包校验和通过，arm64 包实际解包并从包内安装器暂存成功。
- Shell 语法以及 CI / Compose YAML 语法检查。

实际离线 PNG 为 1077 × 1676，232,964 bytes。合成开发集 source nDCG 1、source recall 0.95、必需片段覆盖率 1；不代表真实搜索或事实正确率。

## 交付位置

- [安装器](scripts/install-linux.sh)、[打包脚本](scripts/package-linux.sh)、[部署指南](DEPLOYMENT.md)。
- `dist/searchan-0.1.0-rc.1-linux-amd64.tar.gz` / `dist/searchan-0.1.0-rc.1-linux-arm64.tar.gz`。
- `dist/SHA256SUMS-0.1.0-rc.1`；`bin/searchan` 为开发机 macOS 命令，不要拿它部署 Linux。
- [首轮 GitHub Actions](https://github.com/YoisakiKnd/searchan/actions/runs/38012788280) 已成功，包含 Linux 实际 systemd 安装/升级/回滚、真实 Typst、漏洞扫描和双架构打包。Docker 验证仅手动启用。
- [RC.1 发布工作流](https://github.com/YoisakiKnd/searchan/actions/runs/38013878416) 已通过，[v0.1.0-rc.1](https://github.com/YoisakiKnd/searchan/releases/tag/v0.1.0-rc.1) 已发布，包含双架构安装包及校验和。后续主分支变更不会自动覆盖该发布包。

## 等待真实环境验收

用户已提供 Linux 任务失败日志及官方 DeepSeek 配置，小请求可返回 HTTP 200；我们没有服务器访问权或实际密钥。Linux systemd 启动及升级/回滚已在 CI 通过；修复后的真实搜索 / 模型 / Telegram 全链路、目标主机资源限制、公网 TLS/Webhook、手机显示、多页结果、P50/P95、峰值占用和费用，仍需实测。还需在目标主机演练备份恢复、升级/回滚及磁盘告警，并建立人工标注的真实题集。

暂存测试不改变宿主机的账号、软件包或 systemd。当前没有生产性能、真实正确率或“最佳搜索效果”的验证结论；以上外部验收通过后再记录正式上线结果。
