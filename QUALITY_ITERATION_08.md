# 第八轮：固定最终证据与最小事实回答

2026-10-12，macOS arm64 / Go 1.27.2 / Typst 0.15.1，用户指定的项目外私有配置，DDG + deepseek-flash。改动尚未提交或发布；没有引入参考项目代码、提示词或新 Go 依赖。

## 实现与界限

- 新增 `quality run -fixed-evidence <已有完整运行>` 和仅供本地质量实验的 `Engine.Synthesize`。冻结问题、最终计划、证据顺序/ID/文本/元数据及原运行参考时间；不调用规划、覆盖检查、搜索、Reader、选段或恢复检索。只使用生产生成、核验及全新短总结逻辑，共享六次模型调用上限。
- 源题集、状态、哈希和全部输入界限在请求模型前检查。拒绝改过的输出、缺失/失败/不完整题、重复 ID、非公开 URL、异常字段及超过 120 KiB 的输入；源输出哈希和冻结输入哈希逐题保存。输出不与输入切片共享可修改的数据。模型不收到旧回答、告警或旧核验意见。
- 简单事实题优先一个段落、一至两句核心结论和必要条件，目标最多 120 字符；取消最低字数，避免为凑长度补充机制/历史/配置。简单简介目标最多 220 字符、最多两条补充要点。明确解释、详细、代码、多问或指定长度请求保留一般范围。
- 核验明确包括标题、正文、表格及限制。窄事实题核验失败后，不传入包含具体断言的旧核验意见；直接基于原问题和证据重新生成短总结。没有截断答案、删引用或新增常规模型调用，也没有新增线上硬字数报错。实际模型仍会违反提示，以下保留失败结果。

## 实验

共 24 次固定证据写作实验、13 次完整联网搜索，以及一次真实搜索/模型→队列→Typst→本地模拟 Telegram 上传。没有发送真实群消息，reserve 未运行。所有输出保留在 Git 忽略的独立目录，没有覆盖失败样本。

固定证据来自第七轮最终八题，三个阶段逐题 `evidence_input_hash` 与 `source_answer_hash` 完全一致，搜索和读页计数均为零；原运行参考时间不变，实际重跑时间另记。before 使用第七轮提示，after 加入事实范围与标题核验，minimal 进一步取消凑字数及窄事实核验反馈。模型本身具有随机性，三个阶段同时改变若干写作规则，不能当作严格单因素或稳定因果证明。

| 本地目录 `artifacts/quality/…` | 完成 / 失败 / 缺失 | P50 / P95 | 正文字符 | 单图高度（宽1077） |
| --- | --- | --- | --- | --- |
| `iteration-08-fixed-before` | 8 / 0 / 0 | 3.55s / 7.01s | 43–532 | 805–1396 |
| `iteration-08-fixed-after` | 8 / 0 / 0 | 2.37s / 5.44s | 83–313 | 778–1334 |
| `iteration-08-fixed-minimal` | 8 / 0 / 0 | 2.18s / 5.39s | 41–178 | 695–1001 |
| `iteration-08-live` | 8 / 0 / 0 | 9.69s / 16.38s | 41–227 | 748–1135 |
| `iteration-08-live-cross` | 5 / 0 / 0 | 10.13s / 11.91s | 38–193 | 690–918 |

前两组固定运行的整份事实审阅仍 pending，不纳入准确率对比。最新固定组、两组联网输出均逐题审阅，身份标明 **Codex AI source review (not independent human review)**，保存在各目录 `review.json`，CLI 已校验题目、答案哈希、标准和陈述对应关系。

| 已完成审阅 | 核心回答 | 整份事实通过 / 失败 / 待确认 | 标准通过 | 引用陈述支持 / 不支持 |
| --- | --- | --- | --- | --- |
| 固定 minimal 八题 | 8 / 8 | 3 / 3 / 2 | 15 / 16 | 9 / 1 |
| 完整联网八题 | 7 / 8 | 7 / 1 / 0 | 15 / 17 | 15 / 0 |
| 完整联网跨领域五题 | 4 / 5 | 2 / 2 / 1 | 8 / 10 | 6 / 1 |

13 次联网合计核心回答 11 题、事实通过 9 题、失败 3 题、待确认 1 题。Spring Data JPA 的局部事实虽通过却未回答核心关系，因此不能把九题事实通过当作九题完整可用，或与不同题目/不同证据的固定组直接比较准确率。所有已审题无错实体或判定为无必要拒答；有检索/选段不足，且只说明这次所选资料的局限。

主要发现：

- HTTP、PostgreSQL、Redis 最小答案能保留核心结论；此前的连接池绝对判断及重启推论不再出现在这些最新输出中，尚不能证明后续不会复发。
- context 的标题/正文仍把观察取消写成退出的一般必要条件；取消不强停的核心正确，额外条件过强。
- JWT 固定短总结仍称“默认不加密”，又在限制中说未核实默认。联网正文改为 JWS/JWE 区分，标题却仍给 JWT 下通用默认结论；正文支持不能认证标题。
- RAG 仍从二手教程或百科的典型流程推成普遍必须的向量库流程。固定组有引用原文支持，但原文本身泛化；联网组原文明确说 Typically，模型又丢掉条件。
- 大熊猫不再混成小熊猫，但仍复述不必要的分类分歧；联网短总结把旧“多数中国学者”当作当前事实。原始共识/提议状态未充分核对，未判全题通过。故宫固定组的额外1925/1965历史仍待原始全文核对，且夹入英文；联网最小答案没有扩写精确年份。
- JPA 关系题的计划被分类为 fact，最终选择 Specification 与旧词汇表。发现菜单虽有概览文档，仍未补读；缺失 Repository/JPA/Hibernate 的层级解释。应修复通用关系请求的取证范围及选段，而不是硬编码本题答案。
- MongoDB 的发现查询把明确名称拆成 `Mongo DB`，两个精确概览查询都无结果，其中一次 DDG 挑战。实际靠原查询读到翻译站完成；不能视作原始来源召回成功。技术标识符的拼写变体适合作为补充，不应替代原始名称。

复核入口包括 [Go context](https://pkg.go.dev/context)、[net/http](https://pkg.go.dev/net/http)、[PostgreSQL 隔离](https://www.postgresql.org/docs/current/transaction-iso.html)、[Redis Pub/Sub](https://redis.io/docs/latest/develop/pubsub/)、[RFC7519](https://www.rfc-editor.org/rfc/rfc7519)、[RAG 原始论文](https://arxiv.org/abs/2005.11401)、[Spring Data JPA](https://spring.io/projects/spring-data-jpa/)、[Hibernate ORM](https://hibernate.org/orm/)、[MongoDB 项目](https://spring.io/projects/spring-data-mongodb/)、[Redis 项目](https://spring.io/projects/spring-data-redis/)及 [Smithsonian 大熊猫](https://nationalzoo.si.edu/animals/giant-panda)。这些链接只用于事后审阅，没有注入机器人输入。台北历史全文仍未取得；也没有仅凭论文发表年份否定图谱完成年份。

另执行本地 Go 反例程序，HTTP/1.1 第一响应读到 EOF、尚未 Close 时，第二请求实际 `GotConn.Reused=true`；忽略取消的 worker 也在任务自然完成后正常退出。这只否定上述全称断言，不建议省略 Close，也不推广到所有协议/Transport。程序与 JSON 保存在 `/tmp/searchan-fact-counterexamples.go`、`/tmp/searchan-iteration-08-counterexamples.json`，未作为机器人输入或部署产物。

## 工程验证与限制

最终完整 `sh scripts/verify.sh` 通过：全项目 race/coverage、vet、govulncheck、构建、11题合成回归及真实 Typst；日志 `/tmp/searchan-iteration-08-final-verify.log`。新增回归验证冻结输入不取证、不改 ID/日期、不混入旧反馈，异常源输入在模型请求前拒绝，互斥参数及切片所有权；详细/多问请求不会被窄事实规则压缩。

真实搜索/模型→任务队列→Typst→本地模拟 Telegram 验收通过，任务约8.57s，模型4、搜索3、读页3，实际检查 PNG、caption 来源、话题路由和重复 Webhook。日志 `/tmp/searchan-iteration-08-local-telegram.log`；不代表真实平台或手机验收。

固定 before 出现一次非法 JSON（stop/255 tokens）；联网初组出现两次（stop/259及38 tokens），均经既有一次有界全新生成后完成，没有保存草稿，不推断为 token 截断或已知服务端根因。其他本轮组未记录该问题，不代表已消除。

已查看最新固定组8张和联网13张实际 PNG 联系表，均为单图，无额外空白页或明显溢出；未做真实 Telegram 压缩及手机缩放验收。没有测本轮目标 Linux 峰值 RSS、token成本或端到端并发容量，不用回放耗时声称线上提速。

另构建本地 `dev-iteration-08` Linux amd64/arm64 开发包，位于 `/tmp/searchan-iteration-08-bundles`。实际核对 ELF 架构、SHA-256、公开第八轮报告及私有配置/快照/PNG/AppleDouble 未进入包；commit 明示 unknown，因为改动尚未提交。这不是正式发布或目标 Linux 运行验收。

下一轮先解决关系问题的双方资料、原始名称的发现查询及长文条件段落选择，再用新的完整联网样本检验。已有 HTTP 原文包含 Client.Do 条件却未入最终证据，说明“读到官网”还不够。事实质量仍未通过上线验收；目标 Linux 和真实会话交付仍待用户指定测试环境与目标后实测。
