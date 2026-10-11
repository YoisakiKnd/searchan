# 本地搜索效果测试

`searchan quality` 使用真实生产研究引擎、搜索适配器和模型协议，在本机保存结果供逐条复核。它不启动 Bot、不发送平台消息，也不把配置文件放入产物。普通 `go test` 和 CI 只跑合成 HTTP 测试，不会使用私有配置或付费接口。

首轮已实际执行 3 条联网查询、查看图片并逐条审阅，结果见 [QUALITY_BASELINE.md](QUALITY_BASELINE.md)。完成流程不等于质量合格，本轮仍有长度、来源正文边界和过时事实问题。

## 第一轮固定题集

[quality/cases.json](quality/cases.json) 包含 30 条真实问题：3 条 Spring Data 故障试跑题、17 条开发题、10 条预留题，覆盖名称拼写、相似实体、缩写、中文实体、比较、操作条件、错误前提和时效信息。预留题没有做盲测隔离，不能称作独立盲测。

每条有核心问题复核标准及第一方来源核对入口；这些标准和链接只交给审阅者，**不注入搜索或模型输入**。这是一套初始审阅题集，不是已经完整标注答案和原文位置的 30 条金标准。动态问题须按运行时间重查；不能把当前网页内容回填成历史运行的事实。

先运行三条 pilot，定位具体问题，每次只改一个因素。之后扩展开发题，最后运行预留题，避免只为 Spring Data 调参。

## 运行

```sh
go build -o bin/searchan ./cmd/searchan
./bin/searchan quality list -split all -limit 30
./bin/searchan quality run \
  -env-file "$HOME/.config/searchan/eval.env" \
  -render -out artifacts/quality/baseline-01 -label baseline
```

默认只串行跑 `pilot` 中最多 3 条，不会自动执行全部 30 条。每题使用现有任务、研究、搜索、读页和模型预算。真实模型及搜索服务可能计费。配置文件可使用现有服务的 `KEY=VALUE` 格式；文件内配置覆盖当前环境。不执行 shell，不展开 `$VAR`、命令替换或 `source`；不支持多行值，双引号采用 Go 字符串转义语法，单引号按字面读取。平台配置在测试中被屏蔽。传输层代理环境仍取自启动进程；文件里的 `HTTP_PROXY` 等不会修改进程网络环境。

Linux 发布包包含 `quality/cases.json` 及本说明。从解包目录运行 `./bin/searchan quality list`，或在其他目录使用 `-cases /解包目录/quality/cases.json`；安装服务后题集不自动复制到 `/opt/searchan/current`，可继续使用解包目录中的题集绝对路径。

如果私有配置里的 Typst 路径属于另一台机器，可加 `-typst /本机/typst`。`-render` 才运行实际 Typst；没有此选项时不会声称排版已验收。输出目录必须不存在，以防覆盖已有基线和审阅结果。

```sh
# 单题；指定其他分组的题时使用 -split all
./bin/searchan quality run -env-file "$HOME/.config/searchan/eval.env" \
  -split all -case go-context-timeout -limit 1 \
  -render -out artifacts/quality/context-01

# 开发题，分批执行
./bin/searchan quality run -env-file "$HOME/.config/searchan/eval.env" \
  -split development -limit 5 -render -out artifacts/quality/dev-01
```

## 每题保留什么

| 文件 | 用途 |
| --- | --- |
| `report.json` | 运行模式、题集与可执行文件 SHA-256、Go 版本、安全参数、预算、耗时、状态及各阶段计数 |
| `suite.json` | 本轮题集副本，避免修改题目后混用旧标注 |
| `review.json` | 逐题事实、实体、拒答、标准和引用支持的审阅；初始全部待审，不自动给准确率 |
| `<id>/output.json` | 成功或失败时的研究输出，包括问题、计划、候选、证据、最终回答、告警和诊断 |
| `<id>/snapshot.json` | 供应商解析后的结果、直接/备用 Reader 正文或错误类别；不是原始 HTTP 流量 |
| `<id>/review.md` | 对照复核标准、逐条结论与对应证据原文，便于定位错误 |
| `<id>/page-*.png` | 使用真实 Typst 渲染的最终图片，需人工查看换页、密度及可读性 |

目录 0700、文件 0600，默认置于 Git 忽略的 `artifacts/quality/`。不保存配置原文、API key、HTTP 头、模型请求或服务端原始错误正文；模型及供应商端点只保存摘要。模型名称、协议及所选参数允许写入报告，不应在这些字段中放密钥。正文和搜索结果本身是本地评测数据，分享前应先检查。

每题快照最多 96 个交互、正文/结果 JSON 每交互 2 MiB、总计 16 MiB。达到上限明确标记不完整；不把截断快照当成可复现资料。正文缓存被绕过，减少首次/再次运行的缓存差异。服务端自身缓存、模型版本变化和网络抖动仍可能影响真实运行。新增记录仅存在于本地测试命令，Bot 请求不会收集快照或增加调用。

模型调用失败时，报告的 `model_errors` 最多保留六个固定错误类别，例如 `deadline`、`http_429`、`connection_reset`、`invalid_json` 或 `model_error`；不记录原始错误文本、请求或模型草稿。该字段用于区分失败类型，不能反推出未记录的根因。早期运行未包含此字段，须保留“原因未知”，不能用后来的成功覆盖失败。

模型正文解析可进一步报告 `invalid_json_syntax`、`invalid_json_type_<允许列表字段>` 等类别；`model_output_issues` 只含固定外形类别（object/array/fence/empty/other）、字节数、错误位置和允许列表内字段，不含草稿内容。没有相应诊断的旧失败不能补猜。

## 固定资料重跑总结

```sh
./bin/searchan quality run -env-file "$HOME/.config/searchan/eval.env" \
  -replay artifacts/quality/baseline-01 \
  -case springdata-intro -limit 1 \
  -render -out artifacts/quality/answer-experiment-01
```

回放固定原运行的最终搜索计划，并替换搜索及 Reader I/O；覆盖检查、回答、核验仍调用真实模型。这是在隔离测试后续流程，**不评估新规划器**，也不证明当前联网效果。`stats.model_calls` 是引擎逻辑额度，包含复用计划这一阶段；`live_model_calls` 是实际进入模型 Completer 的次数，不包含底层可能发生的一次 HTTP 重试。因此两者均不是 token 数或物理 HTTP 请求数。

计划/资料快照不能覆盖的新查询或读页请求返回 `snapshot_miss`，整题标记 `incomplete`，不偷偷联网补齐，也不能计作合格答案。修改检索策略或规划器时应另跑真实搜索基线，并检查新资料差异；严格回放未必能覆盖新路径。回放不模拟网络等待，**不能拿回放耗时宣称线上提速**。模型仍有随机性、时间输入仍为本轮时间，固定资料不代表输出必然逐字相同。

## 怎样复核

逐条打开 `review.md` 和 PNG，在 `review.json` 填写审阅者与结论。可由 Codex 先做逐条来源复核，但须标明 AI 审阅身份；正式验收可以再由人独立检查，不能写成已有人类验收。不要让被测模型为自己的输出独自判分。

- `core_answered`：是否回答用户核心问题。不能用模型自己输出的 `answers_query` 代替审阅。
- `entity_correct`：是否答错实体或用相似项目替代。
- `factual_verdict`：`pass/fail/unclear/pending`，独立核实事实与适用条件。旧文章原样支持一项错误陈述时，引用可能通过，事实仍失败。
- `unnecessary_refusal`：当次可取得资料已经足够，却无必要地拒答。检索或访问故障与错误拒答应分开记录。
- `checks`：每项标准填 `pass/fail/pending` 并解释缺失事实。
- `claims`：整个带引用陈述的事实是否被其引用支持，填 `supported/unsupported/unclear/pending`。一个陈述含多个事实时，所有事实都获得支持才能标为支持；这不是逐个原子事实的统计。

```sh
./bin/searchan quality review -out artifacts/quality/baseline-01
```

只汇总已经完整复核的题，未审题独立计数，不按成功或失败填充。回答/题集哈希、标准、结论和引用对应关系发生变化时拒绝复用旧标注。程序报告的 `format_valid` 只说明格式和引用 ID 有效，不代表支持性、真实性或相关性。`completed` 也只表示程序走完，不表示质量合格。

本轮先看原始条数：核心回答、错误实体、事实通过/失败/待确认、无必要拒答、标准通过项和引用支持项。耗时提供所有已尝试题的端到端 p50/p95，单独查看失败题，样本很小时直接报告每题耗时。当前没有暴露模型 token 用量，也没有测进程峰值 RSS；不伪造这两项数据。运行时间包含本地保存和可选渲染。

现有 `searchan eval` 继续负责合成证据的排序和召回回归，不能用它的高分替代这里的真实答案验收。
