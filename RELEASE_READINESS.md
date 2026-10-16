# 发布候选验证记录

2026-10-12，macOS arm64，Go 1.27.2，Typst 0.15.1，govulncheck 1.8.0。状态：用户已开始 Linux 部署联调，完整生产验收尚未完成。

最新本地状态（2026-10-17）：可选三协议原生Schema及本地严格校验已实现，默认关闭；最终全量工程检查通过。同一模型的原始SpringData简介完整实网链路约15秒生成单图，事实复核通过，但举例引用不完整；复杂资料的语义与范围问题仍未闭合，不能正式上线。详见 [QUALITY_ITERATION_34.md](QUALITY_ITERATION_34.md)。此前3463141的[Linux CI](https://github.com/YoisakiKnd/searchan/actions/runs/38938378723)通过安装、升级/回滚和双架构打包；新改动以对应远端运行结果为准。完整质量与目标环境门槛未完成；以下为历史记录。

RC.6 发布准备：包含自适应取证、本地 quality 测试、简单简介写作范围、正文边界及安全 JSON 诊断。本地完整检查通过，新增错误隐私/不重试用例另通过 model/quality race。四份原 HTML 边界检查通过，但第三轮真实模型五次尝试只有两次完成、三次失败，准确性与稳定性仍未通过；详见 [QUALITY_ITERATION_03.md](QUALITY_ITERATION_03.md) 与 [CHANGELOG.md](CHANGELOG.md)。Linux 包补齐公开题集及效果测试说明；私有配置、HTML/正文快照和 PNG 均不发布。远端构建状态以 Actions 为准。

后续已建立本地 `quality` 评测命令及 30 条初始复核题，使用用户指定的私有配置成功运行 DDG + deepseek-flash 三条联网题和一次固定计划/资料回放，并检查实际图片。三条核心问题得到回答，但发现冗长、相关推荐混入及二手资料错误，不能作为上线质量合格结论。记录见 [QUALITY_BASELINE.md](QUALITY_BASELINE.md)。评测记录不接入生产请求路径、不发送平台消息、不保存密钥；新增合成集成测试覆盖快照缺失、限流/正文错误类型、真实模型调用计数、取消、审阅哈希以及可选实际 Typst。全项目验证通过，后续小幅评测工具修改另通过 targeted race/vet。

本次本地待发布改动：正文有效性检查覆盖供应商、直接读取、缓存及备用 Reader；旧缓存中的明确拦截页重新读取，新拦截页不写缓存。正文截断与获取方式传入证据，新增有界逐页诊断。首批最多三页，覆盖充分时单篇也停止补搜；规划开启时也评估单个 aspect；比较另检查可识别的双方实体。明确核心缺口可使用尚未使用的一次补搜机会，并在全阶段六次模型调用上限内重新回答。没有引入参考项目代码、提示词、工具定义或依赖。

完整 `scripts/verify.sh` 已通过：race、vet、govulncheck、全协议/平台回归、构建、11 题离线回放及实际 Typst PNG。新增回归覆盖单篇充分、首批停止、语义缺口补搜、三条正文入口的拒绝/备用读取、缓存拦截页刷新、截断传递、后置纠错与保守短总结、调用/时间/页数限制、对比双方、重复证据引用编号及取消释放。当前合成基准：证据筛选约 0.61ms / 89KB，90 条候选选页约 0.45ms / 101KB，HTML 抽取约 0.125ms / 150KB；不据此宣称真实端到端速度或质量提升。单个 aspect 在启用规划时可能比旧版多一次覆盖调用，总调用上限不变。

本轮针对 `SpringData简介` 的错实体和空白分页修复：保留原查询并加入空格拼写变体；明确复合实体参与选页与证据过滤；没有对应资料时定向补搜，无关摘要不能兜底。生成及核验增加 `answers_query` 判断，未回答核心问题时只生成保守短总结，避免扩写无关目录。本轮方案来自用户失败案例与本项目回归；只查看 Hyw-Frontier 的公开说明作为行为对照，没有引入其代码、提示词或工具流程。

Typst 在 PNG 导出前测量内容高度，普通报告生成一张，确需分页时默认最多三页，极长布局提前拒绝并走文字交付；不解码或拼接位图。Telegram 图片与全部可点击来源同次交付，多页用相册并持久化全部回执。可选 `QQ_BACKEND=onebot` 接入 NapCat / OneBot v11 HTTP，自定义节点合并 PNG、摘要和来源；base64 使用固定缓冲区流式传输。签名、机器人 ID、白名单与重复事件均验证；通用发送异常及异步回执不自动重发。

完整 `scripts/verify.sh` 已通过：race / coverage、vet、govulncheck、编译、11 题离线回归和真实中文 PNG；无新增 Go 依赖。新增测试覆盖实体拼写、错误扩展、相关资料补搜及全无关结果拒绝、未回答核心问题的短总结、真实紧凑排版与长文分页、Telegram 单图 / 相册及全部链接、OneBot 群 / 私聊转发、base64 边界、验签和访问限制、合并交付不明结果的重启恢复，以及旧版逐页记录兼容。示例 PNG 从 1077×1676 缩为 1077×1292，本次示例 229580 bytes，正文未裁剪。

此前 DDG 无凭据探测在开发网络上连接超时；后续使用本地私有配置的真实查询已成功，见文首效果基线。尚未发送真实平台消息，Telegram 手机展示和 QQ 网关权限仍需部署后验收。

最后一轮本机合成基准：8 篇正文证据筛选约 0.60–0.62ms、88KB 分配；90 条候选选页约 0.45ms、98KB 分配。小写词项跳过 CamelCase 额外扫描，没有引入模型重排或常驻语义索引。不同轮次 CPU 状态有差异，以上只记录当前样本，不声称端到端加速或服务器峰值占用。

AI 总结优先行为已随 [v0.1.0-rc.2](https://github.com/YoisakiKnd/searchan/releases/tag/v0.1.0-rc.2) 发布：仅有搜索摘要或独立核验遇到问题时生成一次带限制说明的简短总结；缺少模型、生成失败、格式或引用不合法则报错，不再返回摘录。RC.1 保留旧行为，不覆盖已有版本。

模型兼容与连接恢复已随 [v0.1.0-rc.3](https://github.com/YoisakiKnd/searchan/releases/tag/v0.1.0-rc.3) 发布：OpenAI Chat Completions / Responses、Anthropic Messages 三种模型协议、自定义端点与认证、显式思考配置和有预算的一次临时传输重试。独立模型连接池移除了搜索客户端的 15 秒响应头限制，完整请求仍有 deadline。新增 `model check` 以相同配置检查约 10 KB 合成输入与 JSON 输出。用户提供的连接重置日志不能单独确定故障根因；尚未使用用户的实际凭据验证这次修复。

RC.3 已通过本地完整 `scripts/verify.sh` 及 [Linux 发布工作流](https://github.com/YoisakiKnd/searchan/actions/runs/38032790568)：三种协议均验证首次连接中断、一次恢复、引用校验、实际 Typst PNG 和模拟平台交付；另覆盖截断 / 拒答 / 无效 JSON、认证错误和 429 不重试、取消、重试次数及时间预算。RC.3 模型逻辑调用最多 5 次，默认含重试最多 10 次 HTTP 请求。

当前主分支修复短总结提示词冲突：不再继承完整回答的 300–700 字及章节 / 表格指令，为程序限制说明预留字符。仅布局不合格时在原模型阶段时间内允许一次 AI 修正，引用错误、生成失败及非法 JSON 仍直接报错。布局日志分别报告字符数、章节数和表格存在情况，不保存无效草稿。通常仍最多 5 次逻辑调用，修正时最多 6 次，默认含传输重试最多 12 次 HTTP 请求。

本次完整 `scripts/verify.sh` 通过，新增回归覆盖 300 / 301 字符边界、说明预留、超长 / 章节 / 表格修正、无效引用不修正、一次修正后仍失败、共享 deadline、取消和调用上限，以及修正后的实际 Typst PNG 与模拟平台交付。全项目 race、vet、漏洞扫描、离线回归及中文 PNG 通过，无新增 Go 依赖。真实供应商上能否避免用户这次错误，仍需升级后重新提交原查询验收。

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
