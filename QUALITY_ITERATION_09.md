# 第九轮：完整名称、双方关系取证与聊天入口验收

2026-10-12，macOS arm64 / Go 1.27.2 / Typst 0.15.1，用户指定的项目外私有配置，DDG + deepseek-flash。改动保留本地，尚未提交或发布；未引入外部项目代码、提示词、新 Go 依赖或额外常驻服务。

## 最终保留的修改

- 发现查询优先保留明确写出的完整名称，如 `Spring Data MongoDB`，不拆成 `Spring Data` 与 `Mongo DB` 占用两次发现机会。保留原查询，补充查询最多两条；缩写拼写和完整词边界匹配有回归测试。
- 明确的“甲和乙是什么关系”归为双方关系请求，规划模型标成 fact 也不能丢掉原始范围。只从原问题提取明确的两个名称，不猜第二个对象；普通“关系型数据库”等不走此路径。
- 候选排序、证据相关性、补搜锚定及缺口检查统一处理双方名称，包括 `Hibernate` 这样的单词名称。URL 没有以名称结尾的手册，也能按标题参与双方覆盖；同时提及双方的文章不占据单方专属覆盖位置。时效标志与总搜索、读页、模型调用预算不变。
- 发现查询的构造集中到一个函数，调度和排序使用同一规则。下述纯名称查询实验没有保留为默认，仍使用原有中性概览查询。
- 按用户选择，日常 Telegram 入口为 `/search 问题`、`/search@机器人名 问题`，以及消息开头 `@机器人名 自然语言问题`；帮助补充自然语言示例。中间顺口提及机器人不触发搜索。沿用白名单、话题路由、容量限制和事件去重。

## 三组完整联网实验

共九次质量运行，八次完成并生成单张 PNG，一次格式/引用校验失败；没有覆盖失败样本，也没有运行 reserve。另执行两个真实搜索/模型到本地模拟 Telegram 的入口测试。所有平台上传仅发往本地模拟服务器，没有发送真实群消息。

| 目录 `artifacts/quality/…` | 完成 / 失败 / 缺失 | P50 / P95（全部尝试） | 正文字符（完成题） | 单图高度（宽1077） |
| --- | --- | --- | --- | --- |
| `iteration-09-discovery-live` | 3 / 0 / 0 | 14.44s / 22.23s | 97–400 | 870–1160 |
| `iteration-09-scope-live` | 3 / 0 / 0 | 11.43s / 22.98s | 102–365 | 947–1185 |
| `iteration-09-name-live`（未采用的实验） | 2 / 1 / 0 | 17.41s / 18.02s | 120–291 | 923–1059 |

题目为 Data/Framework 区别、Data JPA/Hibernate 关系、Data MongoDB 简介。第一组修正名称和关系分类；第二组另修正单词名称在下游的范围处理。第三组临时改成只搜索带引号的完整名称，尝试减少 project/overview 等词引来的目录和组织页。每题只有一次运行，模型与在线搜索有波动，不能视为稳定因果证明或整体准确率。

成功输出均逐题复核，审阅身份为 **Codex AI source review (not independent human review)**，`review.json` 已经 CLI 校验哈希、标准和引用对应关系。失败题没有可交付答案，事实审阅仍 pending，单列为运行失败，未补成准确答案。

| 来源审阅 | 核心回答 | 整份事实通过 / 失败 / 待确认 | 严格标准通过 | 引用陈述支持 / 不支持 |
| --- | --- | --- | --- | --- |
| 第一组（三题） | 3 / 3 | 2 / 1 / 0 | 5 / 7 | 7 / 1 |
| 第二组（三题，保留策略） | 3 / 3 | 3 / 0 / 0 | 5 / 7 | 7 / 0 |
| 第三组（两题成功输出） | 1 / 2 | 2 / 0 / 0 | 2 / 4 | 4 / 0 |

“事实通过”不等于完整满足标准：第二组 Data/Framework 没有说明集成关系，MongoDB 未明确指出所属 Data 家族；JPA 使用带限制说明的全新短总结，仍未稳定获得直接来源的完整层级说明。引用检查同时核对实际抓取的页面标题和正文：GitHub 仓库 title 的项目描述也包含模板/轻量级仓库说明，不能因该信息不在某条 Text 片段中便误判引用缺失。

第一组 Data/Framework 复述翻译页的“Spring 框架的子项目”，比较语境下混淆 Framework 与生态；即使二手原文支持，也未判事实通过。其最后一项“生态基础项目”还超出所引段落。第二组未出现该断言，但三题的小样本不足以确认已消除。

纯名称实验让 JPA 实际读到第一方仓库、项目主页与 Hibernate 手册，读页从前两组的八页变为四页，搜索从四次变为三次。然而最终没有解释 Hibernate 实现 JPA：手册约10 KB处明示该关系，选入证据的却是仓库讨论与建表配置。Data/Framework 又出现一次 `answer_validation`，没有可交付答案；现有安全报告只保留错误类别，无法确认具体校验项，不能推断是 token 截断或查询改动导致。因最终回答没有达到采用要求，撤回纯名称默认策略，保留实验记录；不以原始来源数量或少读几页宣称搜索效果提升。

第一组 JPA 出现一次非法 JSON（stop / 363 output tokens），经既有一次有界全新生成完成；第二组 JPA 有一次模型 deadline，最终生成短总结。没有保存模型失败草稿或绕过引用校验。

事后复核入口：[Spring Data](https://spring.io/projects/spring-data/)、[Framework](https://spring.io/projects/spring-framework/)、[Data JPA](https://spring.io/projects/spring-data-jpa/)、[Hibernate ORM](https://hibernate.org/orm/)、[Data MongoDB](https://spring.io/projects/spring-data-mongodb/)、[MongoDB 仓库](https://docs.spring.io/spring-data/mongodb/reference/mongodb/repositories/repositories.html)与[对象映射](https://docs.spring.io/spring-data/mongodb/reference/mongodb/mapping/mapping.html)。这些只用于审阅，没有注入机器人输入、硬编码域名或答案。

## 工程与模拟平台验证

撤回查询实验后的最终 `sh scripts/verify.sh` 全部通过，日志 `/tmp/searchan-iteration-09-kept-verify.log`：全项目 race/coverage、vet、govulncheck、构建、11题合成回归和真实 Typst。另有 research race 回归，覆盖完整名称、双主体关系、单词第二方、原请求保留及重放幂等；沙箱内第一次测试因禁止本地监听失败，随后允许本地监听的测试实际通过，不将沙箱报错当代码失败或略过测试。

真实搜索/模型→队列→Typst→本地模拟 Telegram 两个入口均通过：命令任务约11.59s、开头提及任务约12.07s，均模型4次/搜索3次，读页分别3/4。检查实际 PNG 签名、可点击来源、话题 ID 和重复 Webhook 去重；日志 `/tmp/searchan-iteration-09-local-telegram.log`。未测真实 Telegram 手机显示、压缩或 QQ 网关账户权限。

已打开第二组的三张实际 PNG 检查，无额外空白页、明显文字溢出或裁切；没有把这次三张检查泛称为全部八张逐图视觉验收。资料标题偏长和 lead/要点重复仍有改善空间。

本机合成基准连续三次：证据筛选约0.601–0.607ms、90.3–90.4KB/848次分配；候选选页约0.862–0.872ms、118.7–119.1KB/2374次分配。日志 `/tmp/searchan-iteration-09-bench.log`。这只记录当前实现，不声称比前版更快，也不代表目标 Linux 峰值 RSS 或并发容量。

本地 `dev-iteration-09` Linux amd64/arm64 开发包已生成至 `/tmp/searchan-iteration-09-bundles`，核对了 ELF 架构、SHA-256、第九轮报告及私有配置/测试快照/AppleDouble 排除。版本 commit 明示 unknown，因为工作尚未提交。这不是正式 release 或目标 Linux 运行验收。

## 接下来

1. 用已保存的长手册查明定义、规范关系及必要条件段落如何被目录、代码和重复术语挤掉。先做离线选段对照，再用新的联网输出检查，不增加常规模型调用。
2. 为格式校验失败补充有界、不会泄漏草稿或凭据的原因诊断；保留未知引用及不合法结构报错行为，不放松校验来提高完成率。
3. 在开发题上的策略稳定后才运行预留集，随后继续用户选定的本地及模拟平台验收。真实平台与目标 Linux 实机验收待用户提供环境；目前不能声称正式上线质量已达标。
