# 本地搜索效果测试

最新记录见 [QUALITY_ITERATION_28.md](QUALITY_ITERATION_28.md)：当前模型固定证据回归共四次逻辑调用，PostgreSQL事实有支持但范围未全，JWT仍失败；两份单图和完整文本已逐条AI复核，不是独立人工金标准或新的联网结果。Telegram真实提及/代码示例边界、本地平台及完整工程检查通过。第四至二十八轮整理为开发候选走Linux CI；8条预留题有效结果和完整质量门槛仍未完成。

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

批量运行默认连续两题搜索服务不可用就停止，减少在故障网络下的无效模型调用。成功搜索但无资料、生成/格式失败不累计该计数。报告的 `selected_cases` 是完整选择，`cases` 仅含实际执行题，`unrun_cases` 列出没有执行的题，`stopped_reason` 说明中止；未执行题没有输出或事实审阅，不算完成或事实正确。确需继续诊断时加 `-continue-on-unavailable`，这可能继续产生费用。该策略只用于本地quality批次，不改变Bot任务行为；旧报告缺少这些字段，不能据此补猜是否中止。

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

另有可选的完整服务验收，使用真实搜索/模型与 Typst，经 Webhook、任务队列、持久记录，向本地模拟 Telegram 上传 PNG，检查话题路由、来源 caption 和重复更新。它会丢弃配置中的全部平台凭据，只向测试创建的回环接口发送，不向真实群发消息：

```sh
SEARCHAN_TEST_REAL_ENV=/path/to/private.env \
SEARCHAN_TEST_TYPST=/path/to/typst \
SEARCHAN_TEST_ARTIFACT_DIR=artifacts/quality/local-telegram-01 \
go test -race ./app -run '^TestLiveSearchToLocalTelegramPNG$' -v -count=1
```

此测试需要网络，可能产生搜索/模型费用；默认测试不设置真实配置和Typst时跳过。默认有command、leading-mention两条付费子测试；只验证一次可使用 `-run '^TestLiveSearchToLocalTelegramPNG/leading-mention$'`。可选ARTIFACT_DIR下按子测试名创建新目录，拒绝覆盖；保存最终output.json、实际uploaded.png及含caption、交付状态、尺寸和图片哈希的delivery.json，便于事后事实与画面复核。不会保存配置或凭据；目录本身仍含查询及来源内容，应按私有测试数据保管。它验证真实处理链路，不替代答案事实审阅、真实平台发送或目标 Linux 主机验收。

| 文件 | 用途 |
| --- | --- |
| `report.json` | 运行模式、题集与可执行文件 SHA-256、Go 版本、安全参数、预算、耗时、状态及各阶段计数 |
| `suite.json` | 本轮题集副本，避免修改题目后混用旧标注 |
| `review.json` | 逐题事实、实体、拒答、标准和引用支持的审阅；初始全部待审，不自动给准确率 |
| `<id>/output.json` | 成功或失败时的研究输出，包括问题、计划、候选、证据、最终回答、告警和诊断 |
| `<id>/snapshot.json` | 供应商解析后的结果、直接/备用 Reader 正文或错误类别；不是原始 HTTP 流量 |
| `<id>/synthesis_input.json` | 仅固定证据模式：原问题、最终计划、证据及原运行参考时间，不含旧回答或核验意见 |
| `<id>/review.md` | 对照复核标准、逐条结论与对应证据原文，便于定位错误 |
| `<id>/page-*.png` | 使用真实 Typst 渲染的最终图片，需人工查看换页、密度及可读性 |

格式/引用失败另记录固定 `validation_code`，如 `unknown_citation`、`missing_citation`、`brief_layout`；进度和服务日志使用 `validation`。代码为空表示不是已识别的校验错误，不能推断具体原因。未知 ID 的原始值不写进错误。`snapshot` 中新正文可带最多128个可见HTML标题偏移，旧快照无偏移仍可重放；标题不是事实可信度标签。

目录 0700、文件 0600，默认置于 Git 忽略的 `artifacts/quality/`。不保存配置原文、API key、HTTP 头、模型请求或服务端原始错误正文；模型及供应商端点只保存摘要。模型名称、协议及所选参数允许写入报告，不应在这些字段中放密钥。正文和搜索结果本身是本地评测数据，分享前应先检查。

每题快照最多 96 个交互、正文/结果 JSON 每交互 2 MiB、总计 16 MiB。达到上限明确标记不完整；不把截断快照当成可复现资料。正文缓存被绕过，减少首次/再次运行的缓存差异。服务端自身缓存、模型版本变化和网络抖动仍可能影响真实运行。新增记录仅存在于本地测试命令，Bot 请求不会收集快照或增加调用。

网络故障新增固定类别 `network_timeout` / `network_error`，重放时恢复对应网络错误语义，供后端暂停策略判断，不恢复原始 URL、IP 或错误详情。旧快照仅有 `io_error` 时继续视为原因未知，不能补猜其类型；这种旧记录未必能复现新版按网络故障计数的调度，不能作为该策略的严格因果对照。生产搜索调用的 `error_code` 使用相同安全分类。

模型调用失败时，报告的 `model_errors` 最多保留六个固定错误类别，例如 `deadline`、`http_429`、`connection_reset`、`invalid_json` 或 `model_error`；不记录原始错误文本、请求或模型草稿。该字段用于区分失败类型，不能反推出未记录的根因。早期运行未包含此字段，须保留“原因未知”，不能用后来的成功覆盖失败。

模型正文解析可进一步报告 `invalid_json_syntax`、`invalid_json_type_<允许列表字段>` 等类别；终止错误另分 `output_incomplete`、`answer_missing`、`output_refused`、`output_protocol`。`model_output_issues` 仅含固定类别、外形、大小、错误位置、允许列表内字段/协议/结束状态及有界 token 数，不含草稿或推理内容。核验失败后的保守总结可在结果 `stats.verification_issue` / `verification_output` 保留安全诊断；这些字段不代表答案已经核验通过。没有相应诊断的旧失败不能补猜。

## 固定资料重跑总结

```sh
./bin/searchan quality run -env-file "$HOME/.config/searchan/eval.env" \
  -replay artifacts/quality/baseline-01 \
  -case springdata-intro -limit 1 \
  -render -out artifacts/quality/answer-experiment-01
```

回放固定原运行的最终搜索计划，并替换搜索及 Reader I/O；覆盖检查、回答、核验仍调用真实模型。这是在隔离测试后续流程，**不评估新规划器**，也不证明当前联网效果。`stats.model_calls` 是引擎逻辑额度，包含复用计划这一阶段；`live_model_calls` 是实际进入模型 Completer 的次数，不包含底层可能发生的一次 HTTP 重试。因此两者均不是 token 数或物理 HTTP 请求数。

覆盖检查可根据有界候选 ID 选择补读。快照未记录被新选择的页面时仍计为缺失，不联网补齐；不能把该次回放当成完整效果样本。`stats.coverage_candidates` 记录模型看到了哪些现有线索及通过检查的选择，`candidate_reads` 与 `page_reads` 记录实际读取；区分没搜到、未选中、读取失败、证据未支持及写作错误。该回放仍可能因模型选择不同而改变证据集，并非严格固定最终 evidence 的写作实验。

计划/资料快照不能覆盖的新查询或读页请求返回 `snapshot_miss`，整题标记 `incomplete`，不偷偷联网补齐，也不能计作合格答案。修改检索策略或规划器时应另跑真实搜索基线，并检查新资料差异；严格回放未必能覆盖新路径。回放不模拟网络等待，**不能拿回放耗时宣称线上提速**。模型仍有随机性、时间输入仍为本轮时间，固定资料不代表输出必然逐字相同。

## 严格固定最终证据的写作实验

```sh
./bin/searchan quality run -env-file "$HOME/.config/searchan/eval.env" \
  -fixed-evidence artifacts/quality/baseline-01 \
  -case springdata-intro -limit 1 \
  -render -out artifacts/quality/writing-experiment-01
```

该模式为 `evidence-model-live`，与 `-replay` 互斥。它只运行生成、核验及必要的全新短总结；不运行规划、覆盖检查、搜索、Reader、段落筛选或缺口补搜。问题、最终计划、证据顺序、引用 ID、正文与元数据，以及原运行的 `generated_at` 均冻结。本地冻结输入和输出保留完整元数据；模型输入与生产使用相同精简视图，省去文档哈希、段落位置和排序字段，原文与来源限制不变。模型收到的 `current_date` 与图片参考时间仍为原时间，实际本轮时间见报告 `started`；不将今天的资料补回过去。旧回答、告警和核验反馈不作为输入。

在创建运行目录和请求模型前，检查源题集、完成状态、输出哈希、证据 ID/URL、字段及总量界限。每题记录 `source_answer_hash` 标识原输出，`evidence_input_hash` 标识本轮冻结输入；对照组需逐题两者相同。哈希只验证输入对应关系，不认证来源或事实。源运行必须完整且成功，不能以失败样本中的部分证据冒充基线。任一源输入被修改时拒绝运行，不悄悄重新取证。

此模式继续遵守模型、时间及输出额度，不改变线上 Bot 的 `Engine.Run`。缺证据不会进行恢复检索，不能把它的完成率或耗时当作完整搜索效果。模型自身仍可能变化；单次对照无法证明稳定因果效果。严谨的写作比较应复核各组整份答案，而不能只比较长度或格式。

## 怎样复核

逐条打开 `review.md` 和 PNG，在 `review.json` 填写审阅者与结论。可由 Codex 先做逐条来源复核，但须标明 AI 审阅身份；正式验收可以再由人独立检查，不能写成已有人类验收。不要让被测模型为自己的输出独自判分。

- `core_answered`：是否回答用户核心问题。不能用模型自己输出的 `answers_query` 代替审阅。
- `entity_correct`：是否答错实体或用相似项目替代。
- `factual_verdict`：`pass/fail/unclear/pending`，独立核实事实与适用条件。旧文章原样支持一项错误陈述时，引用可能通过，事实仍失败。
- `unnecessary_refusal`：当次可取得资料已经足够，却无必要地拒答。检索或访问故障与错误拒答应分开记录。
- `checks`：每项标准填 `pass/fail/pending` 并解释缺失事实。
- `claims`：整个带引用陈述的事实是否被其引用支持，填 `supported/unsupported/unclear/pending`。一个陈述含多个事实时，所有事实都获得支持才能标为支持；这不是逐个原子事实的统计。
- `uncited_text`：新建审阅版本2中的标题、章节名、表头及限制说明，逐位置填写 `pass/fail/unclear/pending`。中性标签可通过，事实断言仍需证据及与正文一致的确定程度；任意项待审则整题待审。此处裁决单独计数，不增加带引用陈述的支持条数。

```sh
./bin/searchan quality review -out artifacts/quality/baseline-01
```

只汇总已经完整复核的题，未审题独立计数，不按成功或失败填充。回答/题集哈希、标准、结论和引用对应关系发生变化时拒绝复用旧标注。程序报告的 `format_valid` 只说明格式和引用 ID 有效，不代表支持性、真实性或相关性。`completed` 也只表示程序走完，不表示质量合格。

旧版本1审阅保持兼容，其已审题计入 `legacy_body_only`；版本2已审题计入 `full_text_reviewed`。两者说明复核范围，均不表示事实一定通过。新文字裁决另报 `uncited_text_passed/failed/unclear`，不与 `supported_statements` 相加。输出中的位置、文字、顺序和数量需保持一致，只编辑裁决和note；旧记录不会自动补裁决。

本轮先看原始条数：核心回答、错误实体、事实通过/失败/待确认、无必要拒答、标准通过项和引用支持项。耗时提供所有已尝试题的端到端 p50/p95，单独查看失败题，样本很小时直接报告每题耗时。当前没有暴露模型 token 用量，也没有测进程峰值 RSS；不伪造这两项数据。运行时间包含本地保存和可选渲染。

现有 `searchan eval` 继续负责合成证据的排序和召回回归，不能用它的高分替代这里的真实答案验收。
