# 第十轮：长文结构取证、安全诊断与核验对照

2026-10-12，macOS arm64 / Go 1.27.2 / Typst 0.15.1；使用用户指定的项目外配置，DDG + deepseek-flash。改动保留本地，尚未提交或发布。未引入外部项目代码、提示词、新 Go 依赖或常驻服务。

## 保留的修改

- Reader 在同一有界正文缓冲中记录可见 h1–h6 标题的 UTF-8 字节范围，每份正文最多128个；不复制标题内容，不把目录/隐藏区域当正文。正文从 main 提升为 articleBody 时同步重置标题。精确结构标记 `toc` / `table-of-contents` 排除目录，普通正文中的同名文字仍保留。HTTP 2 MiB、正文64 KiB、token256 KiB及栈深512的限制不变。
- 选段按真实标题切开，再沿用每段1800字节的有界分块；标题的查询相关性只给所属首段有限加权，正文不改写、不拼接不同位置。偏移、顺序、重叠、级别及 UTF-8 边界不合法时整体回退；没有标题的旧快照和 Reader 保留原分块。缓存命名空间更新到 v4，避免继续使用旧目录正文。标题只是结构信号，不能证明来源可信或结论正确。
- 格式/引用错误加入固定类别诊断，quality 产物与任务失败日志记录 `validation`。未知引用不再把模型返回的任意 ID 拼进错误，避免泄露 URL、令牌或草稿。用户错误提示与严格引用校验不变。
- 实际图片检查发现行尾 `[1, 2]` 被拆开；Typst 将引用组作为不可断开的行内盒，同样用于表格。保留原质量产物，在独立目录重排全部八份答案，没有重新调用模型。

以上修改没有增加常规搜索、读页、模型调用或常驻服务；仍共享既有并发、六次逻辑模型调用与时间上限。

## 固定正文的选段对照

输入为第九轮 `iteration-09-name-live/spring-data-jpa/snapshot.json` 的原计划和原正文。另读取一次公开 Hibernate 手册，从真实标题加紧随正文的精确匹配定位，把标题偏移映射到原记录；两组使用相同正文、标题和哈希，只切换这些偏移。没有往模型证据补写事实。

无标题分组没有选入手册中“Hibernate and JPA”的完整定义；新分组选入该段（e6，原段落180），包含实现 JPA 的直接说明。记录在 `/tmp/searchan-iteration-10-sections-probe.json`。这证明该份输入的选段变化，不能证明所有长手册都会改善。

另外用两组各自选段做了一次真实写作对照，保存于 `artifacts/quality/iteration-10-structure-writing/`，搜索/读页均为零：旧分组完成短总结，但把 body 误称搜索摘要，Hibernate 断言所引 e3/e4 也不支持；新分组生成失败，既有一次 JSON 重生成后仍无答案。失败没有覆盖或另跑补成成功。该临时实验只记录 generation 类别，未保存细分 OutputDiagnostic，不能断定具体语法或 token 原因。因此不能声称结构修改已在固定写作对照中证明最终答案提升。

## 完整联网效果与逐条审阅

第一组检查三个技术实体/关系题，第二组检查五个跨领域题，最后单独复测用户原始故障题 `SpringData简介`。九题均完成，各一张 PNG，零快照缺失，没有执行 reserve。图片为1077宽、695–1055高。每题只运行一次，在线搜索与模型波动没有被控制，不将小样本比例解释成整体准确率。

| 目录 `artifacts/quality/…` | 完成 / 失败 | P50 / P95 | 正文字符 | 核心回答 | 整份事实通过 / 失败 / 待确认 | 严格标准 | 引用陈述支持 / 不支持 / 待确认 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `iteration-10-outline-live` | 3 / 0 | 15.10s / 22.76s | 86–173 | 3 / 3 | 3 / 0 / 0 | 6 / 7 | 4 / 0 / 0 |
| `iteration-10-cross-live` | 5 / 0 | 11.16s / 11.92s | 47–175 | 4 / 5 | 2 / 2 / 1 | 6 / 10 | 5 / 1 / 1 |
| `iteration-10-original-live` | 1 / 0 | 16.34s / 16.34s | 174 | 1 / 1 | 1 / 0 / 0 | 2 / 3 | 3 / 0 / 0 |

审阅身份为 **Codex AI source review (not independent human review)**。`review.json` 已经 CLI 校验答案哈希、标准及引用对应。合计九题为六题事实通过、两题失败、一题待确认，核心回答8/9、严格标准14/20；不能把100%完成率称为100%正确。

- JPA 这次选入 Hibernate Javadocs 的 JPA 与 Bootstrapping 正文，回答正确区分仓库抽象、规范与实现。仍依赖二手资料解释 Data JPA，本轮不能归因为标题加权单独起效。Data/Framework 定义正确，但未解释具体集成关系，严格标准仍缺一项；MongoDB 定义、POJO/模板/仓库有实际引用支持。来源复核：[Data JPA](https://spring.io/projects/spring-data-jpa/)、[Hibernate ORM](https://hibernate.org/orm/)、[Spring Data](https://spring.io/projects/spring-data/)、[Framework](https://spring.io/projects/spring-framework/)、[Data MongoDB](https://spring.io/projects/spring-data-mongodb/)。
- 原始 `SpringData简介` 读到并引用官方项目页，使命、总括项目和发布列车事实有支持，不再误答为 Framework 文档目录；但漏掉 Repository 和代表性子项目，反而加入未问的 BOM/Calver 和维护状态说明，严格标准仅2/3。这是该次运行的改善，尚不代表稳定解决简介质量。[Spring Data 项目页](https://spring.io/projects/spring-data/)。
- context 正确回答取消不会强制终止 goroutine，但“必须自己检查信号”把示例做法推广成普遍必要条件；实际所引官方例子还允许 `DoSomething(ctx)` 返回错误后退出，未覆盖可取消 API 等路径。[context 文档](https://pkg.go.dev/context)。
- JWT 仍沿用二手教程的“默认不加密”，未区分 JWS/JWE。RFC 定义及 encrypted JWT 示例已经选入证据；问题出在采纳与核验。所引二手段落确实写了该句，所以引用支持单独记 supported，整份事实记 fail，避免混淆这两个维度。[RFC 7519](https://www.rfc-editor.org/rfc/rfc7519)。
- HTTP Body 关闭责任正确，但没有解释 EOF 与关闭对复用的区别，严格标准未满；Pub/Sub 的投递和重放范围正确。[net/http](https://pkg.go.dev/net/http)、[Redis Pub/Sub](https://redis.io/docs/latest/develop/pubsub/)。
- RAG 的核心检索与生成定义正确，未声称保证消除幻觉；把 Naive RAG 例子的“片段而非整份文档”写成一般思路仍有范围歧义，整份事实及这条引用保守记待确认。[原始 RAG 论文](https://arxiv.org/abs/2005.11401)。

JPA 有一次非法 JSON，安全诊断为 syntax / invalid_token / stop / 305 output tokens，经既有一次全新生成完成；没有把它推断为服务端截断。

## 未采用的核验实验

固定上述已保存的五题答案及证据（context、JWT、Pub/Sub、JPA、RAG），每题各调用一次现有核验及一次“逐项检查引用并查找全部证据中的范围限制”的候选核验，共十次真实模型调用，没有搜索/读页。每对输入哈希相同，prompt 单独留哈希；同样使用6秒阶段预算。记录在 `artifacts/quality/iteration-10-verifier-probe/`，此临时探针未纳入默认产品流程。

原核验五题全部放行。候选核验仍放过 context 的必要条件与 JWT 的默认范围，另因 JPA 标题只是并列名称而误报不支持；耗时从约0.48–0.81秒增加为2.06–2.84秒。候选还返回过不符合预期字段类型的 unsupported 对象；探针用 RawMessage 观察，不等于生产 schema 接受。**没有采用该提示词或声称核验已改善**。本轮没有为失败题硬编码答案、添加域名白名单或更换模型。

## 工程、排版与模拟平台

结构及安全诊断改动后的完整 `scripts/verify.sh` 通过：全项目 race/coverage、vet、govulncheck、构建、11题合成回归及真实 Typst；日志 `/tmp/searchan-iteration-10-verify.log`。另有 reader/research/quality/app race 回归；正文提升、标题UTF-8偏移/128项上限、旧缓存刷新、不合法元数据回退、原文保留、未知引用泄露及安全报告均有回归覆盖。

图片修正后实际 render 包测试通过（含短图、连续中图及有界多页），八份保存答案全部重新渲染成单图。逐图打开原八张检查，无额外空白页或明显裁切；修正后额外打开 context 图确认引用组不再断开，并打开使用最终模板生成的原始故障题单图检查。没有将这几次视觉检查混称为重排后八张均逐图复核。

最终代码的真实搜索/模型→Webhook/队列→Typst→本地模拟 Telegram 两入口均通过，任务约10.94/10.76秒，模型4/5次、搜索各3次、读页各4次；检查PNG签名、话题、可点击来源、重复更新和停机。日志 `/tmp/searchan-iteration-10-local-telegram.log`。未发送真实群消息，未验收手机压缩或 QQ 网关账号。

本机合成基准连续三次：原HTML发现链接抽取约0.149–0.150ms、149632 B；80节约60KB正文含标题抽取约0.252–0.255ms、298968 B；现有无标题证据筛选基准约0.589–0.605ms、91.0KB/856次分配，候选排序约0.850–0.859ms、118.8–119.0KB/2374次分配。日志 `/tmp/searchan-iteration-10-bench.log`。不同fixture不能当作标题功能的相对开销；这不是Linux峰值RSS或吞吐验收，也未宣称整体更快。

## 后续优先级

1. 用冻结输入区分“引用支持”“来源范围冲突”与“实际答题”，先试验减少草稿锚定的核验输入，再决定是否替换现有流程；不要继续只叠加量词提示或让每条陈述额外请求模型。
2. 改善第一方资料在最终回答中的实际采纳，并检查同源多个片段是否压过不同范围的直接定义；保持通用规则与资源预算。
3. 稳定后才运行预留集。继续用户选择的本地/模拟验收；目标Linux与真实平台待测试环境，目前未达到正式上线质量门槛。
