# 第二十一轮：扩展开发题与本地 Telegram 交付验收

继续使用私有配置中的 deepseek-flash，优先速度与费用；没有切换模型、启用思考或扩大超时/调用额度。本轮不改生产生成或核验提示，只扩展真实开发题并补齐实际上传物的验收留档。

## 真实搜索与答案复核

| 问题 | 搜索/读页/逻辑模型调用 | 含渲染耗时 | 复核结果 |
| --- | --- | --- | --- |
| PostgreSQL 默认事务隔离级别 | 3/4/4 | 14.085秒 | 核心事实及引用通过；默认值可配置的补充缺失，严格标准1/2 |
| Redis Pub/Sub 送达与重放 | 2/4/4 | 12.737秒 | 核心事实及引用通过，标准2/2 |

两题都通过补读读到官方文档；PostgreSQL 回答 Read Committed，Redis 回答 at-most-once、失败/断连后丢失及不支持历史重放。[PostgreSQL 官方说明](https://www.postgresql.org/docs/current/transaction-iso.html)与[Redis 官方说明](https://redis.io/docs/latest/develop/pubsub/)用于审阅，没有注入搜索或生成输入。PostgreSQL 不展开快照机制，不能据此证明其能够正确解释快照。

资料位于 `artifacts/quality/iteration-21-live-development/`。版本2审阅覆盖正文引用和无引用标题，标准CLI汇总为：2题事实通过、核心回答2/2、标准3/4、引用支持2/2、标题通过2/2；这是Codex辅助复核，不是独立人工金标准。两张实际PNG为1077×767/124953 bytes、1077×783/117511 bytes；已查看，均为单图，无裁切或额外空白页。

这两题不能推翻前轮JWT统一默认断言和context退出必要条件的已知问题，也不足以推导稳定准确率或速度提升。不能因为JWT的默认措辞错误，就机械禁止所有题目回答默认值。

## 本地 Telegram 完整服务验收

只运行 `leading-mention` 子测试：`@local_test_bot PostgreSQL 默认事务隔离级别是什么？`。使用真实搜索、当前模型及Typst，经Webhook、队列和持久任务，向本地模拟Telegram multipart接口发送一张PNG；私有配置内所有平台凭据被丢弃，未向真实群发送。

约14.040秒完成，3次搜索、4次读页、4次逻辑模型调用。重复同一Webhook只创建一个任务；接收提示与报告各一次，群话题5保留，任务和两项交付均持久记录为完成。实际上传图与任务保存的Typst文件字节一致，SHA-256为 `67ebf66a014c1db76cc2bda3e91d0b75162e8443d0f4ee827df65a82989acfde`。实际上传图1077×821/144342 bytes已查看；caption含同一结论和可点击来源。事实及引用通过，同样缺少可配置性补充。

`app/live_telegram_test.go` 增加可选 `SEARCHAN_TEST_ARTIFACT_DIR`，按子测试名称创建新目录，拒绝覆盖，保存最终output.json、delivery.json及实际uploaded.png。文件0600、新目录0700，不保存配置、凭据、模型草稿或任务临时路径。上传读取上限10MiB，检查PNG元数据及与持久图片的字节一致性；只影响显式启用的测试，不增加生产服务的内存或调用。

本轮留档位于 `artifacts/quality/iteration-21-local-telegram/leading-mention/`，含自定义AI审阅；不是标准quality题集报告。应用层 `go test -race ./app -count=1` 通过；不设置真实配置时跳过付费验收。最后一次全项目race、vet、漏洞检查及构建仍为第二十轮，不能将本轮应用层检查写成重新跑过全项目。工程日志另存 `iteration-21-engineering/`。

本轮合计12次逻辑模型调用，未插桩实际模型HTTP重试，没有token或金额数据。没有测试真实Telegram权限、手机压缩或目标Linux运行；新留档也不代表这些已通过。第四至二十一轮未提交/发布，原完整上线目标仍未完成。后续继续检查其他开发题与既有错误类型，保持当前模型和原调用上限；正式发布仍需完整题集/预留题质量及部署运行验收。
