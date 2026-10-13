# 第十一轮：核验完整性、协议诊断与未奏效的写作实验

2026-10-12，macOS arm64 / Go 1.27.2 / Typst 0.15.1；使用用户指定的项目外私有配置，默认模型仍为 deepseek-flash。第四至十一轮修改保留本地，尚未提交或发布。没有增加 Go 依赖、常驻服务或常规模型调用。

## 保留的修复

- 独立核验必须同时返回非 null 的 `unsupported`、`missing` 和 `answers_query`。缺少问题覆盖结论不能算完成核验；转为从原证据生成一次保守短总结，不发布旧草稿。结果 `stats.verification_issue` 区分 `incomplete_result` 和 `model_error`；有安全的模型输出诊断时另存 `verification_output`。
- OpenAI Chat 的 length、Responses 的 incomplete、Anthropic 的 max_tokens，以及空正文、拒答和不支持的结束状态，统一返回固定类别的类型化错误。仅保留协议、允许列表内的结束状态和有界 token 计数，不保存草稿、推理内容或供应商任意字符串。终止错误不进入 JSON 语法重新生成；Responses incomplete 只报告状态，不能一概判为 token 耗尽。
- 按 OpenAI Docs 官方说明补齐独立的 Chat `message.refusal` 字段：即使 finish_reason 为 stop 或兼容网关同时给出正文，只要有非空拒答标记便不解码/发布正文；null、空字符串及省略字段保留正常回答路径。拒答原文不进入诊断。[Structured Outputs 拒答处理](https://developers.openai.com/api/docs/guides/structured-outputs?api-mode=chat)。
- DeepSeek 官方 Chat 端点的 `MODEL_THINKING=auto` 现在尊重显式 `MODEL_REASONING_EFFORT`：非 none effort 启用思考，none 转成关闭开关；互相冲突的显式配置在发送前报错。未设置 effort 时仍关闭思考，其他网关及协议行为不变。本轮没有启用新的线上默认。

六次逻辑模型调用、单阶段/整体 deadline、一次临时传输重试和一次 JSON 语法/类型重新生成的限制不变。核验超时或不完整之后的短总结仍不是事实正确性认证。

## 固定答案的核验对照

只使用第十轮保存的原问题、答案和证据，不检索、不补写参考答案。临时探针保留输入及提示词哈希，未接入 Bot。部分探针用 RawMessage 观察模型返回，不能等同于生产 schema 校验通过。

| 实验目录 `artifacts/quality/…` | 真实模型调用 | 结果 |
| --- | ---: | --- |
| `iteration-11-verifier-order` | 12 | context、JWT、Redis、JPA 各三组：原输入、仅调整输入次序、先输出证据结论。后两组仍放过 context 的必要条件错误及 JWT 的默认范围错误；仅调整次序还误报 JPA。 |
| `iteration-11-verifier-thinking` | 6 | context、JWT、JPA 各做原配置/低强度思考对照，同为6秒预算。原配置均放行，思考三次均超时。 |
| `iteration-11-verifier-thinking-18s` | 3 | 只将思考预算放宽到18秒。context 在13.21秒完成，仍放过错误；JWT、JPA 约14.76/14.07秒失败，仅记录 io_error，具体根因未知。 |
| `iteration-11-verifier-termination` | 1 | 新安全诊断之后，另做一次 JWT 18秒思考探针，11.08秒完成，仍放过默认范围错误。不能用这次成功改写前两次失败的原因。 |

“先输出证据结论”同时改变提示词、字段名和 schema，不能把结果单独归因为输入次序。没有采用这些候选核验提示词，也没有增加每条陈述的模型请求或默认开启思考。当前未更换用户配置的模型。

DeepSeek 官方文档的服务默认与本项目为延迟主动关闭思考的默认不同；显式配置的行为参见 [Thinking Mode](https://api-docs.deepseek.com/guides/thinking_mode/)。本轮结果只反映这些输入与配置，不能推广到所有思考模型。

## 来源删减：实际生成、核验和 Typst 对照

目录 `artifacts/quality/iteration-11-source-ablation/`。四题各两组，共八次写作：完整原证据作为控制；另一组由审阅者手工保留已知官方 URL 前缀。没有搜索/读页，没有添加事实；原问题、计划、参考时间、段落正文和保留引用 ID 不改。删减组的证据集和输入哈希自然不同，不能称为完全相同输入。

手工选择不是线上来源识别器或产品域名白名单，不能用该实验论证“官方来源过滤”普遍有效。每组保存 input/output/meta 与真实 PNG；自定义来源审阅另存 `review.json`，注明 AI 身份和输入/输出哈希，不冒充标准 quality CLI 或独立人工验收。

| 题目 | 全部证据的输出 | 手工删减后的输出 |
| --- | --- | --- |
| context 取消 | 正确说明不会自动退出，但把自行检查 Done 写成必要条件。 | 仍写“需自行监听……才会退出”，同一事实错误保留。 |
| JWT 加密 | 沿用二手资料的“默认不加密”，引用确实支持该二手原句，事实仍失败。 | 引用 RFC，提到 JWS/JWE，却仍在标题和结尾断言默认不加密；引用不支持这个默认范围。 |
| Redis Pub/Sub | 投递/重放结论正确；“不提供确认”未在所引两段中获得支持，整条复合引用不通过。 | 至多一次及无法重放结论直接获官方投递语义支持。 |
| Spring Data JPA / Hibernate | 区分仓库抽象与 JPA 实现，四条复合引用有支持；正文过长且露出 e1/e2 等内部编号。 | 删除有效二手定义后，官方选段只剩元数据和 Hibernate 定义；仍写出 Data JPA 的定位，却只引用 Hibernate 段落，引用不足。 |

逐份 AI 来源审阅：事实4份通过、4份失败；11条带引用复合陈述中6条支持、5条不支持。该小样本含人工选源，不计入完整联网准确率，也不是预留集成绩。不能只比较八次完成率或图片高度。

范围核对入口：[context](https://pkg.go.dev/context)、[RFC 7519](https://www.rfc-editor.org/rfc/rfc7519)、[Redis Pub/Sub](https://redis.io/docs/latest/develop/pubsub/)、[Spring Data JPA](https://spring.io/projects/spring-data-jpa/)。其中 RFC 定义允许 JWS 或 JWE，不能从签名教程推出规范规定默认不加密。

八次均完成一张 PNG，共18次逻辑模型调用；JPA 两组各发生一次 stop 状态的 JSON syntax / invalid_token，分别为405/284 output tokens，已有一次全新生成后完成。不是服务端截断证据。其余每组2次调用。研究阶段约1.88–5.76秒，未包含 Typst；这是无检索实验，不能作为线上耗时。

实际打开检查 context 删减组、JWT 删减组及 JPA 两组共四张图，没有明显裁切或额外空白页；JPA 控制组的冗余和内部引用编号可见。其余四张只验证生成及尺寸元数据，没有宣称八张均逐图视觉验收。

## 工程与本地 Telegram 验收

最新完整 `scripts/verify.sh` 日志 `/tmp/searchan-iteration-11-final-verify.log` 包含全项目 race/coverage、vet、govulncheck、构建、11题合成回归与实际 Typst PNG，所有步骤通过，扫描为 No vulnerabilities found。新增协议/研究/quality 回归覆盖截断不重生成、拒答、空正文、恶意 stop 字符串不泄露、核验缺覆盖结论以及安全诊断进入结果。

本轮另跑两种真实搜索/模型→Webhook/队列→Typst→本地模拟 Telegram 入口：

| 入口 | 任务耗时 | 模型 / 搜索 / 读页 |
| --- | ---: | --- |
| `/search` | 14.392秒 | 4 / 3 / 4 |
| 消息开头 `@机器人 + 自然语言` | 7.042秒 | 4 / 3 / 3 |

均通过 PNG 签名、话题回复、来源文字、重复更新去重及停机检查；日志 `/tmp/searchan-iteration-11-local-telegram.log`。耗时不是 P95/生产承诺；不发送真实群消息，未测试手机压缩、真实 QQ 账号或目标 Linux 的峰值内存与恢复。

随后补入 Chat `message.refusal` 后，另通过 model/quality/research/app 的 race 回归及 vet，日志 `/tmp/searchan-iteration-11-refusal-tests.log`；覆盖非空拒答与看似有效正文共存、null/空/省略字段正常返回、诊断不泄露以及不重试。该最后增补只用模拟协议验证，没有再调用付费模型或重复真实搜索。

## 下一步

当前证据中有正确范围，模型写作和核验仍共同漏检；继续堆叠同类提示或按域名删证据没有得到可靠改善。保留原默认，优先做有区分能力的模型/写作对照，严格记录失败；如更换配置模型或增加相应费用，应遵循用户的选择，不偷偷修改私有配置。改善后再运行预留集。当前仍未达到正式上线质量门槛。
