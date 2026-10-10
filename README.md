# Searchan

Go 搜索 Bot：多后端检索 → 读取网页 → 筛选证据 → 带引用总结与核验 → Typst 渲染 PNG → Telegram / QQ 回复。不读取群聊历史。

已实现六个独立搜索适配器、直接 HTTP / Jina Reader、可配置的 Chat Completions JSON 模型客户端、证据与引用校验、固定 Typst 模板、平台 Webhook / 发送适配、有界持久任务队列和发送记录。真实供应商、模型效果及 Bot 账号权限仍需在部署环境联调；模拟测试不代表搜索质量评测。

Telegram 首发使用 Linux + systemd，提供原生安装、升级/回滚脚本和 amd64/arm64 打包工具，无需 Docker。部署、预检、备份和验收步骤见 [DEPLOYMENT.md](DEPLOYMENT.md)。尚未配置真实服务器与凭据，本地通过不等于已经上线。

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

# 搜索、阅读、总结和渲染；无模型时明确返回资料摘录。
./bin/searchan ask -providers ddg,brave -out artifacts/result 'Go context 如何取消并发任务？'
./bin/searchan ask -text 'Go context 如何取消并发任务？'

# 本地配置诊断，不调用付费接口、不输出密钥。
./bin/searchan doctor -strict
./bin/searchan serve
./bin/searchan healthcheck
./bin/searchan version
```

`ask` 生成 `output.json` 和 `page-1.png` 等图片。JSON 保存搜索计划、段落证据、文档哈希、引用绑定、来源、警告和阶段耗时。离线示例见 [示例图片](artifacts/demo/page-1.png)。

模型配置使用完整 `MODEL_ENDPOINT`（例如自己的 `/v1/chat/completions` 地址）、`MODEL_NAME` 和 `MODEL_API_KEY`。接口须支持普通 Chat Completions 与文本 JSON 输出；其他协议需新增适配器。若不支持 `response_format`，设置 `MODEL_JSON_MODE=false`。无模型时不会把搜索摘要包装成综合结论。

## 搜索与证据策略

1. 保留原始查询；模型可提出最多两个补充查询、四个必须回答的方面。轮流分配查询与启用后端，避免首个查询吃完预算。
2. 有界调度检索，每个任务对同一后端最多保留一个在途请求；认证失败、限流或挑战会在后续查询发出前暂停该后端，剩余额度可用于其他已启用后端。每次最多请求 15 条候选。RRF 融合保留到选页排序，同一后端的多个查询不会叠加投票；DDG 和 SearXNG 中已知的 DDG / Brave 上游也会折算。查询新覆盖、BM25、融合分数和域名多样性共同决定读哪些页面。
3. 流式读取 HTML / 文本，跳过导航、脚本、hidden / aria-hidden 和模板内容，优先 article/main（包括短正文）。保留行内标识符的连接、pre 代码换行和表格列边界，保留文章内的 header 标题；支持常见的省略闭合标签，不构建 DOM。默认不用浏览器。可显式启用 Jina Reader 作读取失败时的备用服务。
4. 正文哈希与完全相同的段落去重；把短事实与相邻正文合并到 ≤1800 bytes 的连续片段。英文词、中文单字/双字、C++ / C#、版本和标识符参与 BM25 排序；MMR 式选择兼顾方面覆盖和内容新颖性。固定大小 shingle 指纹只用于近似内容降权，数字、否定和条件变化保守保留。当前没有语义 reranker 或 PDF 读取。
5. 搜索和抓取账本跨轮复用：同一“后端＋查询”不重复调用，同页不同锚点及默认端口只抓一次。补搜优先未用后端，遇认证、限流或挑战错误则在本次任务中暂停该后端；补搜也能继续读取首轮未读候选。仅补一轮，缺口查询影响该轮选页排序；额度是上限，不强制用完。覆盖模型不可用时，已规划方面的词项缺口也可触发一次补搜；词项命中不证明事实已获得支持。正文和标题均不匹配查询时不直接作为证据，摘要降级也沿用选页排序。
6. 模型生成受限 Answer JSON，程序验证每条事实引用的 evidence ID。独立支持性检查发现问题时最多修订一次，再核验；仍失败返回摘录。核验调用失败会在答案中显示限制。
7. 程序生成来源 URL 和引用编号，固定 Typst 模板仅以文字读取模型输出。生成 1–3 页 PNG，另发可点击来源。

RRF 和多次命中不代表事实正确或来源独立。模型核验是辅助机制，不能代替人工评测。发布日期保留供应商原值，抓取时间单独记录。

“最新 / 今天 / latest”等时效问题优先重新读取网页，不直接沿用供应商正文摘录；内置缓存最多沿用 30 秒。时效要求独立记录为 `plan.requires_fresh`，不会因“核实 / 比较”等意图或模型重新分类而失效；英文按词边界识别，避免把 concurrent 误判成 current。抓取时间不代表原文更新时间。规划失败时保留原始查询并用简单规则识别意图，不擅自添加未经核实的日期或版本。

独立阶段超时：搜索请求 8s、页面读取及备用 Reader 总计 10s、规划 4s、生成 / 修订各最多 18s。整体研究截止时间始终优先；检索阶段为回答保留最多 15s，生成也给核验留时间。慢后端超时后仍利用已收到的正文，尽量返回有依据的结果。参数见 `.env.example`。

`output.json` 新增 `stats.calls`（逐次后端、查询、候选数、新文档数、耗时与错误）、直接 / 备用读取调用数、独立正文数、补搜查询及前 20 个排序候选。证据带连续段落范围、排序分数和方面词项命中；这些字段不代表事实可信度。日志仍只记录任务 ID 和汇总数据。

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
- QQ：设 `QQ_APP_ID`、`QQ_APP_SECRET`，在官方平台配置 `https://你的域名/webhooks/qq`。支持 URL 校验握手、Ed25519 验签、群 / C2C 事件、文件上传和回复。需开通相应类型、订阅和群 / 用户权限；回复窗口、条数及媒体权限需按账号实际联调。未接个人 QQ / OneBot 协议。
- 默认需为每个启用平台配置 `SEARCHAN_ALLOWED_USERS` 或 `SEARCHAN_ALLOWED_CHATS`（`telegram:ID` / `qq:ID`，允许用户或会话按“或”判断）；公开使用须显式 `SEARCHAN_PUBLIC=true`。
- `GET /healthz` 检查进程；`GET /readyz` 检查持久化写入和停机状态，返回排队/运行数量。探测最多缓存 5 秒，不表示外部服务可用。

命令：`/start`、`/help`、`/search 问题`、`/ask 问题`、`/deep 问题`、`/status 任务ID`、`/sources 任务ID`、`/cancel 任务ID`。`/deep` 将搜索 / 阅读预算各翻倍（各最多 24），共用全局并发和任务截止时间，可能增加费用。只能在原会话 / 话题查询任务；取消还须同一用户。

任务先落盘再入队；重复搜索事件不重复建任务，短控制回复也在发送前持久化去重。发送前记录意图、成功后记录回执。渲染失败或首张图片被明确拒绝时降级文字；明确 429 拒绝最多重试两次、单次等待不超过 5 秒；网络超时、5xx、缺失回执等结果不明时进入 `uncertain`，不自动重发。已经发送部分图片后失败也不会重发整份答案。

重启恢复队列与就绪任务；已保存的研究结果可继续渲染/发送；尚未保存结果的搜索 / 生成崩溃标记失败，避免重复付费；发送中崩溃标记 `uncertain`。目前没有自动核对远端或人工重发命令，可查看状态后重新提问。单进程独占数据目录，第二个进程启动报错。领取任务先持久化再出队，写入失败保留队列；取消排队任务立即释放容量。SIGTERM 先停止接收，再给在途工作默认 30 秒宽限。

## 占用与预算

| 项目 | 默认值 |
|---|---:|
| 每用户 / 会话未完成任务上限 | 2 / 4 |
| 同时处理 / 等待任务 | 2 / 32（满时返回 503） |
| 全局搜索 / 正文 / Typst 并发 | 3 / 3 / 1 |
| Webhook 同时处理 | 32 |
| 标准搜索调用 / 阅读尝试上限 | 6 / 8 |
| 模型调用上限 | 6（规划、覆盖、生成、核验、一次修订及复核） |
| 网页响应 / 正文 | 2 MiB / 64 KiB |
| 证据 | 12 段，每段 ≤1800 bytes |
| 答案 / 图片 | 12 KB 内容预算，最多 3 页 |
| 整体任务 / 研究 / 编译超时 | 90s / 45s / 15s |

复用 HTTP 连接、流式解码供应商 JSON、流式解析正文、流式上传图片。Go 只检查 PNG 头，不解码全图。供应商响应在慢后端结束前就收缩到 15 条候选和受限字段；截断字段复制后保留，避免短前缀引用整块超长字符串。正文缓存写磁盘，固定 32 个互斥通道合并同 URL 并发读取，等待期间响应取消；TTL 30 分钟，服务启动及每小时清理。结束任务保留 7 天，`uncertain` 保留待核对，需要运维清理；磁盘仍需容量监控 / 配额。

任务和发送记录使用原子 JSON 文件、fsync 和目录锁，减少数据库依赖；适用于单机小到中等并发。多实例 / 大规模队列需替换存储接口。持久 Bot 服务支持 Linux / macOS；Windows 可用 CLI。

同一用户在同一会话两秒内重复提交会被限流；服务默认不为被限流消息启动搜索。首次本地健康检查后空闲服务 RSS 约 12.6 MiB，仅为该机器上的启动样本，不能代表负载峰值。

未设置 `GOMEMLIMIT` 时主程序设置 96 MiB 的 Go GC 软预算，它不是 RSS 硬上限，也不包含 Typst 子进程。systemd 示例对整个服务设 384 MiB 硬限制，仍须实测调参；可选 Compose 使用同样限制。

开发机 Apple M4、Go 1.27.2 合成基准：约 25 KB HTML 抽取本轮前 0.10 ms、284 KB 分配，本轮后约 0.12 ms、150 KB 分配（分配量减少约 47%，分配次数 943 → 630）。增加了空白、代码、表格与隐藏内容处理，耗时略有增加。新证据筛选在相同的 8 篇正文输入上约 0.55–0.65 ms、87 KB 分配；改动前约 0.30 ms、338 KB。本次以额外的本地计算换取排序、覆盖与冗余判断，分配量降低约 74%，没有声称算法耗时更短。`B/op` 是调用分配量，不是常驻内存；不包含联网、模型或 Typst。复测：`make bench`。

网页读取只允许 HTTP(S) 公网 80/443，DNS 解析验证 IP 后连接，重定向重新校验。阻断内网、元数据与地址转换网段，不使用环境 HTTP 代理绕过检查。支持 UTF-8 HTML / 文本，复杂页面可显式用备用 Reader。

## 离线评测

```sh
# 无网络、模型或平台费用。
./bin/searchan eval -cases evaluation/cases.json -out artifacts/evaluation.json

# 也可评分保存的真实搜索结果，每题一个 <题目ID>.json。
./bin/searchan eval -cases /path/to/labeled-cases.json -replays /path/to/results
```

内置 10 个合成开发样例，覆盖短事实、稀有标识符、版本、语言区别、转载、数字与否定冲突、方面覆盖、无关域名和中文。使用相同来源的 aliases 折算来源组，计算 source nDCG、source recall 和必需文字片段覆盖率。回放真实 Output 时另报候选 nDCG@20，并校验问题与结果匹配。

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
