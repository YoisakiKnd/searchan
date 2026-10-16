# Searchan

本地真实搜索、AI 总结及 Typst 效果测试见 [QUALITY_EVALUATION.md](QUALITY_EVALUATION.md)：固定 30 条复核题，默认先跑 3 条，保存搜索/正文快照和最终图片，并逐条检查事实与引用支持。

最新实测见 [QUALITY_ITERATION_33.md](QUALITY_ITERATION_33.md)：补齐参考链接位置、保留实际引用路径，并在现有预算内最多主动补读一页。完整工程检查通过；新 DDG 单题约15秒生成单图且读到原始规范，但当前模型仍混用二手错误与原文条件，核验和 JSON 稳定性未达上线标准。

Go 搜索 Bot：多后端检索 → 读取网页 → 筛选证据 → 带引用总结与核验 → Typst 渲染 PNG → Telegram / QQ 回复。不读取群聊历史。

已实现六个独立搜索适配器、直接 HTTP / Jina Reader、OpenAI Chat Completions / Responses 与 Anthropic Messages 模型客户端、证据与引用校验、固定 Typst 模板、平台 Webhook / 发送适配、有界持久任务队列和发送记录。真实供应商、模型效果及 Bot 账号权限仍需在部署环境联调；模拟测试不代表搜索质量评测。

Telegram 首发使用 Linux + systemd，提供原生安装、升级/回滚脚本和 amd64/arm64 打包工具，无需 Docker。部署、预检、备份和验收步骤见 [DEPLOYMENT.md](DEPLOYMENT.md)。本机已有用户指定的私有配置用于实测，目标服务器及真实会话交付仍待验收，本地通过不等于已经上线。

本轮检查记录与尚待真实环境验证的项目见 [RELEASE_READINESS.md](RELEASE_READINESS.md)。

安装包从 [GitHub Releases](https://github.com/YoisakiKnd/searchan/releases) 下载：推送 `v*` 版本 tag 后，Actions 在测试及 Linux 安装验收通过后自动构建 amd64 / arm64 包与校验和；候选版本标记为 prerelease。普通提交只生成 Actions artifact。发布操作见 [部署指南](DEPLOYMENT.md)。

## 本地运行

需要 Go ≥1.26、Typst 0.15.1 和中文字体。Linux 建议 Noto Sans CJK；macOS 模板也尝试 Heiti SC / Songti SC。只有 `golang.org/x/net` 一个直接 Go 依赖，无浏览器、Python、Redis 或数据库服务。

```sh
go build -trimpath -ldflags='-s -w' -o bin/searchan ./cmd/searchan

# 离线生成真实 PNG，无需 API 凭据。
./bin/searchan demo -typst /path/to/typst -out artifacts/demo

cp .env.example .env
# 编辑后加载，程序不自动读取 .env。
set -a
source .env
set +a

# 原始搜索结果；选项放在查询前。
./bin/searchan -providers ddg 'Go context cancellation'
./bin/searchan -providers searxng,brave -limit 20 'Go 并发控制'

# 搜索、阅读、AI 总结和渲染；必须配置模型。
./bin/searchan ask -providers ddg,brave -out artifacts/result 'Go context 如何取消并发任务？'
./bin/searchan ask -text 'Go context 如何取消并发任务？'

# 本地配置诊断，不调用付费接口、不输出密钥。
./bin/searchan doctor -strict
./bin/searchan serve
./bin/searchan healthcheck
./bin/searchan version
```

`ask` 生成 `output.json` 和 `page-1.png` 等图片。JSON 保存搜索计划、段落证据、文档哈希、引用绑定、来源、警告和阶段耗时。运行上述 `demo` 后可查看本地生成的 [示例图片](artifacts/demo/page-1.png)。

模型配置使用完整 `MODEL_ENDPOINT`、`MODEL_NAME` 和 `MODEL_API_KEY`。支持以下三种非流式文本协议，也可使用兼容它们的自建服务或网关；URL 原样使用，保留路径前缀和查询参数，不自动补路径。未配置模型时，ask / Bot 搜索直接报错，doctor 也不通过；独立原始搜索命令仍可使用。

| 协议 | 完整端点示例 | `MODEL_API` | 默认认证与输出额度 |
| --- | --- | --- | --- |
| OpenAI Chat Completions | `https://api.openai.com/v1/chat/completions` | `chat_completions` | Bearer；官方 OpenAI 使用 `max_completion_tokens`，其他地址使用 `max_tokens` |
| OpenAI Responses | `https://api.openai.com/v1/responses` | `responses` | Bearer；`max_output_tokens` |
| Anthropic Messages | `https://api.anthropic.com/v1/messages` | `anthropic` | `x-api-key`、`anthropic-version: 2023-06-01`；`max_tokens` |

`MODEL_API=auto` 根据 URL 尾部 `/responses`、`/messages` 识别协议，否则按 Chat Completions 处理。网关使用自定义路径时显式指定协议。`MODEL_AUTH` 可选 `auto`、`bearer`、`x-api-key`、`api-key`，只发送选定的认证头；例如使用 Bearer 的 Anthropic 网关需设置 `MODEL_AUTH=bearer`。Chat 网关如要求新 token 参数，设置 `MODEL_TOKEN_PARAMETER=max_completion_tokens`。依赖 AWS 签名、Google OAuth 等独立云认证的入口，需要先接兼容网关，当前客户端不自动获取这些凭据。

`MODEL_JSON_MODE=true` 在 Chat 使用 `response_format`，在 Responses 使用 `text.format`；不支持 JSON 模式的网关可关闭该选项。Anthropic 不发送这些 OpenAI 字段，依靠统一的 JSON 提示词与本地格式、引用校验。三种协议均拒绝空回答、截断、拒答及工具调用；Responses 从所有消息文本块提取正文，Anthropic 跳过思考块，不把思考过程当答案。

思考控制默认 `MODEL_THINKING=auto`：未知服务不发送厂商专用思考字段，官方 `api.deepseek.com` 的 Chat 请求在未设置 effort 时显式关闭思考，节省本项目有限的时间与输出额度。在该端点显式设置非 `none` 的 `MODEL_REASONING_EFFORT` 会启用思考；`none` 转换成关闭思考，不作为不支持的 effort 值发送。显式关闭思考与非零 effort 冲突时在发请求前报错。可显式设置 `enabled` / `disabled`；`adaptive` 仅供支持该模式的 Anthropic 模型。官方 OpenAI Chat 使用 `MODEL_REASONING_EFFORT` 控制推理；Responses 使用其原生 `reasoning.effort`；Anthropic 使用 `thinking` 和可选 `output_config.effort`。`MODEL_THINKING_BUDGET` 仅用于 Anthropic 的 enabled 模式，默认 1024，须小于 `MODEL_MAX_TOKENS`。参数是否可用、effort 的取值范围取决于具体模型；不支持时应去掉配置，不会自动改协议或降级掩盖错误。

`MODEL_RETRIES=1` 默认只对连接重置、响应传输中断及无 Retry-After 的 502 / 503 / 504 重试一次，等待 200 ms，且要求当前阶段至少剩余 2 秒。重试共享原 deadline；认证错误、429、非法 JSON、输出截断不会重试。重试可能产生第二次计费请求，设为 0 可禁用。模型使用独立连接池，取消搜索客户端的 15 秒响应头限制，以阶段超时和 HTTP 总超时控制完整请求；搜索、阅读和平台发送的超时保持独立。

按实际配置发起模型自检（会调用模型，可能计费；不搜索或发送平台消息）：

```sh
./bin/searchan model check -timeout 30s
# Linux 服务账号与相同 EnvironmentFile：
sudo /opt/searchan/bin/searchan-admin model check -timeout 30s
```

自检发送约 10 KB 合成资料并校验 JSON，仅输出就绪状态、输入大小与耗时；不会打印密钥或模型原始回答。它验证模型通路，不代替真实搜索和 Telegram 交付验收。

## 搜索与证据策略

首批最多读取三页，获得证据后进行必要的覆盖评估；没有可用证据时分批继续读取预留范围内的候选。一篇合适的资料足够时，不再仅因正文少于两篇补搜。启用模型规划后，单个 aspect 也评估一次；关闭规划、只有一个 aspect 的非比较/核实/时效请求保留低开销路径。比较问题另检查可识别的双方实体，防止只找到一方就停止。

覆盖检查复用原有一次模型调用，最多呈现 20 条尚未读取的线索，保留各查询前列结果及少量已读页面中的参考链接。模型可选择至多两个已有候选 ID 补读；程序不接受生成的 URL，继续共用页数、并发、SSRF 检查和截止时间。线索只构建一次，不递归爬取，标题自称“官方”不能证明身份；补读失败时仍可按原规则补搜核心缺口。读到新正文也不代表答案已经核实。

供应商正文、直接读取、缓存和备用 Reader 共同检查明确的验证页、错误页及登录拦截内容；HTTP 200 不代表正文有效。旧缓存中的明确拦截页会重新读取，新拦截页不会写入缓存。启用 Jina Reader 后，正文无效也可触发备用读取，两种读取仍共享单页 deadline。规则不判断事实真假，也不能识别所有模板或伪造页面。

1. 保留原始查询；自动生成 CamelCase / 空格拼写变体，例如 `SpringData简介` → `Spring Data 简介`。模型扩展与拼写变体共计最多三个查询、四个必须回答的方面。扩展不能丢弃原问题中的明确复合实体。轮流分配查询与启用后端，避免首个查询吃完预算。
2. 有界调度检索，每个任务对同一后端最多保留一个在途请求；认证失败、限流或挑战会在后续查询发出前暂停该后端，剩余额度可用于其他已启用后端。每次最多请求 15 条候选。RRF 融合保留到选页排序，同一后端的多个查询不会叠加投票；DDG 和 SearXNG 中已知的 DDG / Brave 上游也会折算。查询新覆盖、BM25、融合分数和域名多样性共同决定读哪些页面。
3. 流式读取 HTML / 文本，跳过导航、脚本、hidden / aria-hidden 和模板内容，优先 article/main（包括短正文）。保留行内标识符的连接、pre 代码换行和表格列边界，保留文章内的 header 标题；支持常见的省略闭合标签，不构建 DOM。默认不用浏览器。可显式启用 Jina Reader 作读取失败时的备用服务。
4. 正文哈希与完全相同的段落去重；把短事实与相邻正文合并到 ≤1800 bytes 的连续片段。英文词、中文单字/双字、C++ / C#、版本和标识符参与 BM25 排序；MMR 式选择兼顾方面覆盖和内容新颖性。固定大小 shingle 指纹只用于近似内容降权，数字、否定和条件变化保守保留。当前没有语义 reranker 或 PDF 读取。
5. 搜索和抓取账本跨轮复用：同一“后端＋查询”不重复调用，同页不同锚点及默认端口只抓一次。补搜优先未用后端，遇认证、限流或挑战错误则在本次任务中暂停该后端；补搜也能继续读取首轮未读候选。仅补一轮，缺口查询影响该轮选页排序；额度是上限，不强制用完。覆盖模型不可用时，已规划方面的词项缺口也可触发一次补搜；词项命中不证明事实已获得支持。正文和标题均不匹配查询时不直接作为证据，摘要降级也沿用选页排序。
6. 模型生成受限 Answer JSON，程序验证每条事实引用的 evidence ID。仅有搜索摘要时，由 AI 生成简短总结并注明未读取原文。独立支持性核验发现问题或调用失败时，基于证据重新生成一次保守短总结，注明关键事实待确认；不沿用原完整回答，也不声称短总结已经通过事实复核。短总结使用独立提示词，只用 lead 一个段落，含标题及程序添加的限制说明合计最多 300 个 Unicode 字符，不生成章节或表格。若引用和通用结构有效、仅短总结布局不合格，且剩余时间至少两秒、调用预算足够，允许 AI 基于原证据修正一次；两次生成共享一个模型阶段预算，不截断正文，不删除事实凑长度。未配置模型、生成失败、通用格式或引用不合法、修正后仍不合格、没有可用证据均明确报错，不再回退为资料摘录。
7. 程序生成来源 URL 和引用编号，固定 Typst 模板仅以文字读取模型输出。Typst 先测量正文高度，普通报告按内容生成一张 PNG，超过约 2992px 才分页，默认最多三页；不会先渲染无限长图片再拼接或裁剪。Telegram 单图附带可点击来源，确需多页时使用一个相册；QQ OneBot 使用一份合并聊天记录。旧 QQ 官方接口保持原发送方式。

明确复合实体（如 `SpringData` / `Spring Data`）参与选页优先级与证据过滤，`Spring Framework`、`SpringDatabase` 不能仅因共享词项成为其介绍证据。无对应正文时优先保留额度定向补搜；无关摘要也不能兜底。模型生成和独立核验均检查 `answers_query`。未回答核心问题且提供具体 missing 事实时，只有未补搜过、有剩余页数、时间和模型额度，才回到检索并根据新证据重新生成一次；没有新证据则保留原证据编号并生成短总结，不循环生成。格式、引用及生成错误继续报错，不能把无关目录扩写成报告。实体规则只覆盖可识别的英文复合名称，并非通用实体消歧或事实验证。

RRF 和多次命中不代表事实正确或来源独立。模型核验是辅助机制，不能代替人工评测。发布日期保留供应商原值，抓取时间单独记录。

同一任务内，某后端连续两次网络故障、请求超时或 HTTP 5xx 后暂停，剩余额度交给其他已配置后端；成功响应（包括正常无结果）重置计数。暂停跨补搜轮次保留，下一个任务重新尝试。挑战、认证错误和限流沿用立即暂停；请求参数类错误不因此禁用后端。此策略减少持续故障时的重复等待，不增加重试或总请求上限，也不能让不可达服务恢复。

简单事实题优先一至两句核心结论及必要条件，目标不超过 120 字符，没有最低字数；简单简介目标不超过 220 字符、最多两条补充要点。它们是写作提示，不是新的线上字数报错或截断规则。明确要求解释、详细、代码、多个问题或指定长度时保留一般写作范围。核验同时检查标题与正文；窄事实题核验失败后，短总结从原问题和证据重写，不携带可能新增断言的具体核验意见。现有真实测试说明模型仍会违反范围或泛化二手资料，不能把这些规则当作正确性保证。

单后端且非比较请求在模型规划前执行原始查询，并复用其结果和预算。后端因认证、限流或挑战在本任务中暂停且无候选时，跳过无效规划；成功空结果或单次临时故障仍允许规划扩展。比较和多后端维持先规划的调度；不额外发健康探测请求。

“最新 / 今天 / latest”等时效问题优先重新读取网页，不直接沿用供应商正文摘录；内置缓存最多沿用 30 秒。时效要求独立记录为 `plan.requires_fresh`，不会因“核实 / 比较”等意图或模型重新分类而失效；英文按词边界识别，避免把 concurrent 误判成 current。抓取时间不代表原文更新时间。规划失败时保留原始查询并用简单规则识别意图，不擅自添加未经核实的日期或版本。

独立阶段超时：搜索请求 8s、页面读取及备用 Reader 总计 10s、规划 4s、生成 / 短总结各最多 18s。整体研究截止时间始终优先；检索阶段为回答保留最多 15s，生成给核验留时间，核验也为短总结留时间。慢后端超时后仍利用已收到的正文，尽量返回有依据的结果。参数见 `.env.example`。

`output.json` 中 `answer_mode` 区分 `summary` / `brief_summary`，`answer_reason` 记录短总结原因（`snippets_only`、`verification_failed`、`verification_unavailable`、`question_unanswered`），`stats.answer_repairs` 记录布局修正次数（至多一次，零次时省略）。`stats.calls` 记录逐次后端、查询、候选数、新文档数、耗时与错误，另含直接 / 备用读取调用数、独立正文数、补搜查询及前 20 个排序候选。证据带连续段落范围、排序分数和方面词项命中；这些字段不代表事实可信度。布局错误日志包含实际字符数、章节数及是否有表格，不记录模型原始回答。

逐页诊断位于 `output.json` 的 `stats.page_reads`：URL、最终读取方式、accepted / duplicate / rejected / failed 状态、固定问题代码、供应商正文拒绝原因、备用读取原因、截断标记和耗时。另有 `reading_batches`、`coverage_checks`、`recovery_trigger`（`evidence_gap` / `answer_gap`）。记录随页数有界，不重复保存整页正文；证据的 `read_method` 和 `truncated` 传入模型，获取方式不表示可信度。所有模型阶段共享最多 6 次逻辑调用，传输重试仍受原 deadline 控制。

`stats.coverage_candidates` 最多记录 20 条实际提供给覆盖模型的候选 ID、URL、来源类型和 `selected`；后者表示已有 ID 通过选择及页数检查，实际是否开始读取与结果以 `page_reads` 为准。`candidate_reads` 是这条路径实际消耗的页数，失败页也计入，零次省略。诊断不保存模型理由、草稿或重复正文。

## 独立搜索后端

| 名称 | 接入方式 | 必需配置 | 限制 |
|---|---|---|---|
| `ddg` | 独立 DuckDuckGo HTML | 无 | 仅首页、最多 30 条；挑战页报错，非稳定官方 API |
| `brave` | 独立 Brave Web Search API | `BRAVE_API_KEY` | 每页最多 20 条，页码 1–10 |
| `tavily` | Search basic | `TAVILY_API_KEY` | 单次最多 20 条，不请求供应商生成答案 |
| `exa` | Search auto + highlights | `EXA_API_KEY` | 适配器单次最多 100 条，实际取决于服务 |
| `searxng` | 自建 JSON API | `SEARXNG_ENDPOINT` | 完整 `/search` 地址，上游分页能力各异 |
| `jina` | Search `s.jina.ai` | `JINA_API_KEY` | 默认前 5 个结果，无分页 |

只有显式选中的后端才会被调用；不会自行启用收费服务、绕过验证码或无限重试。非分页后端收到 `page > 1` 明确报错。原始搜索 CLI 的 `-limit` 是每后端上限，聚合保留整个去重池；研究流程另有调用和证据预算。

SearXNG 的 `settings.yml` 需允许 `search.formats: [html, json]`。可设 `SEARXNG_ENGINES=duckduckgo,bing`，Bing 经自建实例调用。本项目未实现独立 Bing API。DDG 在开发网络的真实检索曾超时，部署后需要测可达性与成功率。

`JINA_READER_ENABLED=true` 单独启用 `r.jina.ai` 正文读取，需要 `JINA_API_KEY`。Search 和 Reader 是不同调用，限额 / 费用以供应商账户为准。

Go 库入口：`search.NewProvider(name, config)`、`NewDDG`、`NewBrave` 等；`search.SearchAll(ctx, providers, request)` 并行聚合并保留部分成功结果。可注入 HTTP Client 测试。Endpoint / Client 属于可信运维配置，不接收聊天用户指定的代理或内部地址。

## Telegram / QQ

默认监听 `127.0.0.1:8090`；公网入口使用自己的 HTTPS 反向代理并保持验签。

- Telegram：设 `TELEGRAM_BOT_TOKEN`、32–256 字符的 `TELEGRAM_WEBHOOK_SECRET`、`TELEGRAM_BOT_USERNAME`，用 `searchan telegram webhook -url https://你的域名/webhooks/telegram` 注册。默认不丢积压消息；只有显式 `-drop-pending` 才会丢弃。用 `telegram info` / `doctor -online` 读取身份和回调状态。接收普通消息、忽略 Bot 消息及发给其他 Bot 的命令，保留回复关系和话题 ID。
- QQ 官方：默认 `QQ_BACKEND=official`，设 `QQ_APP_ID`、`QQ_APP_SECRET`，在官方平台配置 `https://你的域名/webhooks/qq`。支持 URL 校验握手、Ed25519 验签、群 / C2C 事件、文件上传和回复。需开通相应类型、订阅和群 / 用户权限；回复窗口、条数及媒体权限需按账号实际联调。
- QQ OneBot：显式设置 `QQ_BACKEND=onebot` 及 `ONEBOT_ENDPOINT`、`ONEBOT_ACCESS_TOKEN`、`ONEBOT_WEBHOOK_SECRET`、`ONEBOT_SELF_ID`。连接自行部署的 NapCat 等 v11 HTTP 网关，事件上报到 `/webhooks/onebot`；支持 HMAC-SHA1 验签、群 / 私聊、单份合并转发（图片和全部来源）、流式 base64 图片上传。HTTP 上报消息应使用 array 格式；设置步骤见 [部署指南](DEPLOYMENT.md#qq-onebot--napcat-可选)。不自动安装网关，不替用户登录 QQ。
- 默认需为每个启用平台配置 `SEARCHAN_ALLOWED_USERS` 或 `SEARCHAN_ALLOWED_CHATS`（`telegram:ID` / `qq:ID`，允许用户或会话按“或”判断）；公开使用须显式 `SEARCHAN_PUBLIC=true`。
- `GET /healthz` 检查进程；`GET /readyz` 检查持久化写入和停机状态，返回排队/运行数量。探测最多缓存 5 秒，不表示外部服务可用。

Telegram 群聊可以发送 `/search 问题`、`/search@机器人名 问题`，或在消息开头发送 `@机器人名 自然语言问题`。后者需正确配置 `TELEGRAM_BOT_USERNAME`，且 Telegram 必须向 Bot 投递该消息：普通非管理员 Bot 的隐私模式可能阻止普通 @文本。`telegram info` 显示全局隐私能力，`doctor -online` 提示限制；关闭 BotFather 隐私模式后已有群需重新加入，管理员场景则不同。检查通过也不能代替目标群实测，详见 [部署说明](DEPLOYMENT.md)。消息中间的普通提及不触发搜索，权限与额度规则相同。[官方投递规则](https://core.telegram.org/bots/features#privacy-mode)。

QQ OneBot/NapCat 也支持消息开头真实 `@本机器人 自然语言问题`，仅 @机器人 显示帮助。网关须上报 array 消息；字面 `@QQ号` 或原始 CQ 字符串不代替真实 at 段，句中提及不触发自然语言搜索。白名单和额度规则与 `/search` 相同。

命令：`/start`、`/help`、`/search 问题`、`/ask 问题`、`/deep 问题`、`/status 任务ID`、`/sources 任务ID`、`/cancel 任务ID`。`/deep` 将搜索 / 阅读预算各翻倍（各最多 24），共用全局并发和任务截止时间，可能增加费用。只能在原会话 / 话题查询任务；取消还须同一用户。

Telegram 群内推荐 `/search@本Bot 问题`。已送达的消息若以 `@本Bot` 开头，也支持直接写问题；隐私模式下这种普通文本不保证送达，详见 [Telegram FAQ](https://core.telegram.org/bots/faq#what-messages-will-my-bot-get)。普通聊天和句中提及不会自动搜索。空命令、无效任务 ID、队列满等会给出帮助或状态反馈，重复更新仍持久去重。

任务先落盘再入队；重复搜索事件不重复建任务，短控制回复也在发送前持久化去重。发送前记录意图、成功后记录回执。渲染失败、整份合并交付或首张图片被明确拒绝时降级文字；明确 429 拒绝最多重试两次、单次等待不超过 5 秒；网络超时、5xx、缺失回执等结果不明时进入 `uncertain`，不自动重发。已经发送部分图片后失败也不会重发整份答案。相册所有 message ID 一起记录在 `report` 回执；升级时沿用已有逐页发送记录。OneBot `async`、缺失回执、通用发送异常（如 NapCat 1200）保守记为不明结果，不能自动转文字重复发送。

取消可停止排队、研究、渲染准备以及仅受理提示在途的任务；已失败、最终结果发送、发送结果不明或已完成任务不能改成已取消。首次成功取消会优先确认，重复取消仍限速去重，不刷新保留时间；已交给平台的内容不能撤回。

重启恢复队列与就绪任务；已保存的研究结果可继续渲染/发送；尚未保存结果的搜索 / 生成崩溃标记失败，避免重复付费；发送中崩溃标记 `uncertain`。目前没有自动核对远端或人工重发命令，可查看状态后重新提问。单进程独占数据目录，第二个进程启动报错。领取任务先持久化再出队，写入失败保留队列；取消排队任务立即释放容量。SIGTERM 先停止接收，再给在途工作默认 30 秒宽限。

研究失败时，任务文件的 `failure` 保存搜索/读页/模型调用数及固定搜索故障类别计数；日志 `search_failures` 同步输出类别。不会把未完成答案放入可恢复交付的 `output`，也不保存这些故障的原始远端文本。旧任务没有此字段，不能据此推断历史故障原因。

## 占用与预算

| 项目 | 默认值 |
|---|---:|
| 每用户 / 会话未完成任务上限 | 2 / 4 |
| 同时处理 / 等待任务 | 2 / 32（满时返回 503） |
| 全局搜索 / 正文 / Typst 并发 | 3 / 3 / 1 |
| Webhook 同时处理 | 32 |
| 标准搜索调用 / 阅读尝试上限 | 6 / 8 |
| 模型逻辑调用上限 | 全阶段合计最多 6，包含后置检索纠错后的生成及布局修正 |
| 模型 HTTP 请求上限 | 默认最多 12（每次逻辑调用至多重试一次，仍受阶段及总时间限制） |
| 网页响应 / 正文 | 2 MiB / 64 KiB |
| 证据 | 12 段，每段 ≤1800 bytes |
| 答案 / 图片 | 12 KB 内容预算，最多 3 页 |
| 整体任务 / 研究 / 编译超时 | 90s / 45s / 15s |

复用 HTTP 连接、流式解码供应商 JSON、流式解析正文、流式上传图片。Go 只检查 PNG 头，不解码全图。供应商响应在慢后端结束前就收缩到 15 条候选和受限字段；截断字段复制后保留，避免短前缀引用整块超长字符串。正文缓存写磁盘，固定 32 个互斥通道合并同 URL 并发读取，等待期间响应取消；TTL 30 分钟，服务启动及每小时清理。结束任务保留 7 天，`uncertain` 保留待核对，需要运维清理；磁盘仍需容量监控 / 配额。

任务和发送记录使用原子 JSON 文件、fsync 和目录锁，减少数据库依赖；适用于单机小到中等并发。多实例 / 大规模队列需替换存储接口。持久 Bot 服务支持 Linux / macOS；Windows 可用 CLI。

同一用户在同一会话两秒内重复提交会被限流；控制回复额度允许时提示稍后重发。被限流事件持久去重，即使暂不发送反馈，也不会在平台重投或服务重启后变成新搜索；已有搜索不被覆盖。首次本地健康检查后空闲服务 RSS 约 12.6 MiB，仅为该机器上的启动样本，不能代表负载峰值。

未设置 `GOMEMLIMIT` 时主程序设置 96 MiB 的 Go GC 软预算，它不是 RSS 硬上限，也不包含 Typst 子进程。systemd 示例对整个服务设 384 MiB 硬限制，仍须实测调参；可选 Compose 使用同样限制。

开发机 Apple M4、Go 1.27.2 合成基准：约 25 KB HTML 抽取本轮前 0.10 ms、284 KB 分配，本轮后约 0.12 ms、150 KB 分配（分配量减少约 47%，分配次数 943 → 630）。增加了空白、代码、表格与隐藏内容处理，耗时略有增加。新证据筛选在相同的 8 篇正文输入上约 0.55–0.65 ms、87 KB 分配；改动前约 0.30 ms、338 KB。本次以额外的本地计算换取排序、覆盖与冗余判断，分配量降低约 74%，没有声称算法耗时更短。`B/op` 是调用分配量，不是常驻内存；不包含联网、模型或 Typst。复测：`make bench`。

网页读取只允许 HTTP(S) 公网 80/443，全部 DNS 地址先校验再连接字面 IP，重定向重新校验。DNS 与连接共享五秒预算，双栈最多两个地址族竞速，300毫秒后回退，取消并关闭落败连接；TLS/正文仍受原有读页 deadline 限制。阻断内网、元数据与地址转换网段，不使用环境 HTTP 代理绕过检查。支持 UTF-8 HTML / 文本，复杂页面可显式用备用 Reader。

## 离线评测

```sh
# 无网络、模型或平台费用。
./bin/searchan eval -cases evaluation/cases.json -out artifacts/evaluation.json

# 也可评分保存的真实搜索结果，每题一个 <题目ID>.json。
./bin/searchan eval -cases /path/to/labeled-cases.json -replays /path/to/results
```

内置 11 个合成开发样例，覆盖短事实、稀有标识符、版本、语言区别、转载、数字与否定冲突、方面覆盖、无关域名和中文。使用相同来源的 aliases 折算来源组，计算 source nDCG、source recall 和必需文字片段覆盖率。回放真实 Output 时另报候选 nDCG@20，并校验问题与结果匹配。

当前开发样例的 nDCG 为 1、必需片段覆盖率为 1、来源召回均值 0.95；一个 top-1 样例标注了两个相关来源，所以其召回是 0.5。这是用于开发和回归的合成集，已经用于调整规则，**不是保留测试集，不代表真实搜索召回、事实正确率或模型引用支持率**。真实题集和人工核验仍需继续建设。

算法参考：[BM25](https://www.staff.city.ac.uk/~sbrp622/papers/foundations_bm25_review.pdf)、[MMR](https://aclanthology.org/X98-1025/)。本项目采用带字段、标识符和方面权重的轻量变体，默认不新增语义模型服务。

## Linux 安装与验证

```sh
bash scripts/package-linux.sh 0.1.0-rc.1
# 在 Linux 的发布包目录安装；已有可信 Typst 时：
sudo bash scripts/install-linux.sh --typst /usr/local/bin/typst --env-file production.env --install-deps --start

go test -race -cover -timeout 90s ./...
go vet ./...
go test -run '^$' -bench . -benchmem ./reader ./research

# 实际编译测试；不设置时跳过。
SEARCHAN_TEST_TYPST=/path/to/typst go test ./render
```

安装器保留配置和数据，按版本保存程序与 Typst，切换失败恢复旧激活；默认独立用户运行。`--destdir` 可在临时目录暂存，不操作系统服务。本机已运行 race、实际 Typst、模拟 Telegram 上传和安装/升级/回滚暂存测试；首轮 GitHub Actions 也已通过 Linux 实际 systemd 安装、升级、回滚和双架构打包，不调用付费接口。Dockerfile / Compose 保留为可选部署，容器检查仅手动启用，见 [deploy/DOCKER.md](deploy/DOCKER.md)。

下一轮需扩展人工标注题集和真实接口录制，对比召回、结论正确性、引用支持率、P50/P95 和费用，再决定是否增加语义 reranker。当前没有“全网最好”或某个真实正确率的实测结论。路线与待办见 [PLAN.md](PLAN.md)。

## 官方接口参考

- [Tavily](https://docs.tavily.com/documentation/api-reference/endpoint/search)
- [Exa](https://exa.ai/docs/reference/search)
- [Brave](https://api-dashboard.search.brave.com/api-reference/web/search/get)
- [SearXNG](https://docs.searxng.org/dev/search_api.html)
- [Jina Reader / Search](https://github.com/jina-ai/reader)
- [Typst](https://typst.app/docs/)
- [Telegram](https://core.telegram.org/bots/api)
- [QQ 官方参考实现](https://github.com/tencent-connect/qqbot-nodejs)
