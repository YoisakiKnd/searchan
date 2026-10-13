# 第二十九轮：当前模型的评测用量记录

2026-10-13。遵循用户选择，只用现有deepseek-flash、原预算和超时，不启用更强模型或思考模式。本轮零真实模型调用、零真实搜索、零平台消息。

## 已提交候选的Linux验收

第四至二十八轮已提交到 `codex/readiness-hardening-20261013`，提交 `ef615a2d5cbeae9b68597a3ca7fbf5bc693c9520`。[该提交的Linux CI](https://github.com/YoisakiKnd/searchan/actions/runs/38337812937) completed/success：race/coverage、vet、漏洞扫描、构建、合成回放、实际Typst、systemd安装/升级/回滚及amd64/arm64打包均通过；Docker任务跳过。没有合并、tag或新正式发布，RC.6未被覆盖。

这证明该候选的工程检查，不能证明事实准确性或目标主机资源/真实账号权限。JWT/context等已知范围问题及8条预留题的有效联网结果仍未通过。

## 证据核对与本轮修改

再次核对第二十八轮JWT固定输入：RFC证据e2/e5已经给出JWS/JWE定义与不同形式，不是关键定义漏读。模型仍选择教程中未限定适用形式的说法。本轮不改证据顺序、排序或提示词，也不声称范围错误改善。

此前quality只记录逻辑Complete次数，不能说明传输重试或token用量。现在增加可选 `ObservedCompleter`；正常Bot仍使用原 `Completer`，不收集记录或额外解析usage。本地quality的每题 `report.json` 增加有界 `model_requests`，最多六项，顺序对应实际逻辑调用；回放规划不会伪造记录，不支持观察的自定义模型保持兼容。

每项仅保存协议、适配器请求尝试数、JSON输入字节数、耗时、外层响应/输出JSON是否完整解码及数值用量字段。没有端点、凭据、提示词、用户问题、原文、草稿或供应商自由文本。仅完整解码的最后一个响应提供usage；失败输出仍可带用量，读到一半或丢失的响应保持未知。缺失/null/非法计数不填零；真实返回的零保留。

`request_attempts` 是适配器请求循环次数，不是TCP发送数、服务器收到的次数或账单次数；HTTP Transport内部重试不可见。`input_json_bytes` 不含系统提示和协议外壳，不是完整请求字节或token数。`output_decoded` 只说明JSON成功解码，不说明引用或事实通过。失败请求可能产生费用，不能把缺失usage解释为免费；本轮不计算价格，也不补猜旧报告用量。

保留协议原生计数：[OpenAI Chat Completions](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create) 的prompt/completion、[Responses用量说明](https://developers.openai.com/api/docs/guides/token-counting)中的input/output；reasoning和cached明细不重复加总。[Anthropic缓存说明](https://platform.claude.com/docs/en/build-with-claude/prompt-caching#tracking-cache-performance)明确input_tokens不包含另报的缓存创建/读取计数，因此分别保存，不混成通用输入总量。三协议以本地fixture验证；本轮未验证DeepSeek服务实际返回值。DeepSeek官方页面抓取未成功，没有据此扩展供应商字段或缓存策略。

## 已完成的验证

- 模型/quality专项race通过，包括三协议、零/缺失/非法用量、截断/非法输出、断流重试、取消、并发隔离、回放不计请求、自定义Completer兼容及报告落盘；quality集成使用实际Typst。
- model/quality的go vet、程序构建和11条合成检索回放通过。测试初次编译缺少新接口，另一次出现夹具名称错误，均在最终专项成功前修正；不把它们称为已有生产缺陷的红绿回归。
- Apple M4、Go1.27.2、固定本地transport微基准：从ef615a2复制旧客户端和当前客户端，在隔离临时目录交替执行before/after/before/after，各两次500ms。旧路径6102–6362ns/op，新普通路径6159–6399ns/op；两组均81allocs/op、约14312B/op。没有测出明显额外分配，不声称真实网络加速或服务器峰值占用改善。最初并行/短样本噪声较大，不用于结论。

日志、对照源码和审计记录在忽略目录 `artifacts/quality/iteration-29-engineering/`。没有把私有配置或评测产物提交到版本库。

## 首次未执行与后续完成

首次完整 `bash scripts/verify.sh` 的自动审批失败：审批服务提示额度耗尽，操作未执行，不是安全拒绝。当时没有完整检查日志，改动未提交。随后提示的恢复时间过后重新申请获执行，完整检查exit0；当前模型正常PG固定证据生成/核验的真实用量也已进入quality报告。后续记录见 [第三十轮](QUALITY_ITERATION_30.md)。第二十八轮的绿色Linux CI仍不能代表新提交的Linux验收，远端以对应提交的新Actions为准。

用量确认不代表事实质量通过；PG范围标准仍未全，JWT候选仍失败。继续优先定位能由原资料复现的检索缺口，不扩大付费批次，不修改用户选择的模型。正式质量与目标环境门槛保持未完成。
