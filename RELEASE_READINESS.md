# 0.1.0-rc.1 本地发布检查

2026-10-10，macOS arm64，Go 1.27.2，Typst 0.15.1，govulncheck 1.8.0。状态：可供 Linux 部署验收的候选版本，尚未实际上线。

后续主分支已调整为 AI 总结优先：仅有搜索摘要或独立核验遇到问题时生成一次带限制说明的简短总结；缺少模型、生成失败、格式或引用不合法则报错，不再返回摘录。该变更已通过本地 `scripts/verify.sh`，新增短总结真实 Typst → 模拟 Telegram PNG 交付、错误回复及最多 5 次模型调用的回归检查。以下 RC.1 发布包保留原版本行为，后续变更需通过新 tag 发布。

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

用户尚未准备服务器、域名和凭据。Linux systemd 启动及升级/回滚已在 CI 通过；目标主机资源限制、公网 TLS/Webhook、真实搜索/模型鉴权、指定 Telegram 会话的 PNG 与引用交付、手机显示、多页结果、P50/P95、峰值占用和费用，仍未实测。还需在目标主机演练备份恢复、升级/回滚及磁盘告警，并建立人工标注的真实题集。

暂存测试不改变宿主机的账号、软件包或 systemd。当前没有生产性能、真实正确率或“最佳搜索效果”的验证结论；以上外部验收通过后再记录正式上线结果。
