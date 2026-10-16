# 第三十一轮：DDG恢复后的真实全链路验收

记录2026-10-13的实际运行；不把当时的网络状态或动态版本结论当作之后日期的保证。模型仍是用户配置的deepseek-flash，预算、超时和思考配置不变，无真实平台消息。

## 当前提交与免费网络诊断

`4a12d36`的[Linux CI](https://github.com/YoisakiKnd/searchan/actions/runs/38353934953)通过。随后`156ea74`增加手动启用的免费DDG诊断及部署说明；[该提交普通push检查](https://github.com/YoisakiKnd/searchan/actions/runs/38355674330)的linux任务通过，实网诊断和Docker跳过。没有合并、tag或新正式发布。

[手动诊断38355722098](https://github.com/YoisakiKnd/searchan/actions/runs/38355722098)实际运行现有DDG适配器，只查询`SpringData简介`一次，返回10项、无错误；常规Linux和Docker任务跳过，没有模型/Bot凭据。本机同一公开查询随后也返回10项、exit0。结果主要是Spring Data相关教程和翻译镜像，不能把十项结果等同于十个第一方来源或事实质量通过；先前“当前DDG不可达”的判断已不再成立。

## 真实搜索、读取与出图

原始`SpringData简介`完整运行约11.9秒：3次搜索、4页读取、4次模型调用，生成1077×936/175599 bytes单图。确实读到了Spring Data官方概览，核心定义正确，不再只描述Spring Framework目录。但附加句把“包含许多数据库专项子项目”扩大为组成范围，并把发布列车BOM范围压到整个项目，整体未通过事实复核。

随后运行八题批次，六次完成、两次报错。核对历史记录发现批次包含了已测试的未知模块负例，漏了原先只有故障记录的context.Value题，已另跑该题；因此是九条不同预留题，不是新的完整十题预留集，也不是盲测。加原始问题共十次真实全链路运行，八次单图、两次报错。

| 题目 | 流程与复核结果 |
| --- | --- |
| Spring Data Commons职责 | 核心职责准确；附带二手资料观察未引用实际e2，不能整体视为引用通过 |
| findBy方法名查询原理 | 首次及唯一语法重生成都返回无效JSON，报generation，未发布草稿 |
| PostgreSQL Serializable | 重试整个事务有支持；二手来源的“唯一串行顺序”条件错误，不等于规范所需的某个串行顺序 |
| Redis Streams/Pub/Sub | 保留了主要比较、未承诺exactly-once；交付条件和部分限制文字缺少所给证据支持，待确认 |
| JWT解析与身份验证 | “不能仅信任解码”正确；JWS范围扩大、必要声明检查遗漏，整体失败 |
| 未知LunarDB模块 | 8页后no_evidence，未编造模块或替换实体；安全负例通过不等于生成了正确答案 |
| 最新Go与日期 | 版本号正确，错误地以VERSION文件time字段证明发布日期，标题也错误 |
| 最新Spring Data发布列车 | 所给依赖文档和复核时官网支持2026.0.1及版本范围，复核通过 |
| context.Value可选参数 | 所引Go原文直接支持请求范围数据及不传可选参数的限制，复核通过 |

最新Go题的保存输入给出2026-10-02时间戳，但[官方发布历史](https://go.dev/doc/devel/release)在本轮复核时明确给出go1.27.2于2026-10-08发布。该参考仅用于审阅，没有回填模型输入。PostgreSQL的[规范说明](https://www.postgresql.org/docs/current/transaction-iso.html)要求与某个串行顺序等价，不要求唯一顺序；二手来源的字面引文存在不能证明事实正确。JWT另按[RFC8725](https://www.rfc-editor.org/rfc/rfc8725.html)核查范围；Spring Data动态结果按[官网](https://spring.io/projects/spring-data/)及当时保存的原文复核。

## 用量、审阅与限制

十次真实运行共29次搜索调用、41页读取、39次逻辑模型调用及39个适配器请求尝试。服务报告输入96210、输出4692、total_tokens100902；cached_tokens9344为明细，不重复加总。两次免费网络探测另计，不混入29次研究搜索。没有金额估算，适配器尝试也不是服务器收到的次数或账单次数。

所有已尝试题的p50约10.7秒、p95约18.5秒，样本只有十次且包含失败，不能作为生产性能SLA。八张图均已查看，正文与来源完整，没有空白尾页；两个失败任务无图片。

三份版本2审阅覆盖所有正文、标题、节标题及限制，`quality review`均无待审项。九条预留记录合计事实pass3、fail3、unclear3；原始Spring Data另为fail。流程完成、引用文字存在、核心问题被提及和事实正确分别统计，不以未输出错误实体或安全拒绝充当正确答案。审阅由Codex AI完成，不是独立人工金标准。

结果在忽略目录 `iteration-31-live-springdata/`、`iteration-31-reserve-live/`、`iteration-31-reserve-context/`；网络结果、图片哈希、用量汇总及复核脚本在 `iteration-31-engineering/`。私有配置、快照、原文和图片不提交到代码库。

## 下一步

检验独立最小格式重生成能否减少findBy的JSON失败；保留同一原始证据、一次重生成、共享deadline与总调用上限，不宽松解析、不使用无效草稿。此方向尚未证明失败率改善。另检查已读英文文章中的直接原始文档链接，以及机器版本时间戳误作发布日期的取证缺口；不加入答案/网站字典。正式质量、独立人工复核、目标Linux资源/恢复及真实会话/手机验收仍未完成。
