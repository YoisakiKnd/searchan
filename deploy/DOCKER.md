# 可选 Docker 部署

默认原生安装流程见 [DEPLOYMENT.md](../DEPLOYMENT.md)。以下命令均在源码根目录执行。Docker 验证在 CI 中仅由 workflow_dispatch 的 docker 选项启用，不是原生首发的前提。

2026-10-10。首发使用 Telegram 官方 Webhook，单机单进程部署。

当前代码已提供访问控制、任务恢复、发送去重、平台限流重试、真实中文 PNG、优雅停机、健康检查和部署预检。**这份文档不是已上线证明**：用户尚未准备服务器、域名和真实凭据；外网交付及真实搜索质量验收留待环境具备后执行。Docker daemon 当前未运行，容器构建和 Caddy 验证已配置进 CI，但本机尚未执行。

## 1. 准备环境

需要 Linux 主机、Docker Engine 与 Compose v2、指向主机的域名、可用的搜索/模型接口以及 Telegram Bot 凭据。网络必须可访问所选搜索源、模型和 api.telegram.org。主机开放 80/443 给 Caddy；8090 只绑定本机。

复制 `.env.example` 为 `.env`，权限设为 600；填写：

- `TELEGRAM_BOT_TOKEN`、`TELEGRAM_WEBHOOK_SECRET`、`TELEGRAM_BOT_USERNAME`。
- `SEARCHAN_ALLOWED_USERS=telegram:你的数字用户ID`，或 `SEARCHAN_ALLOWED_CHATS=telegram:群ID`。允许用户与允许会话按“或”判断；Bot ID 不能代替用户 ID。默认不公开服务；有意开放时才设 `SEARCHAN_PUBLIC=true`。
- `MODEL_ENDPOINT`、`MODEL_NAME`，以及接口需要的 `MODEL_API_KEY`。
- `SEARCHAN_PROVIDERS` 及对应凭据。只填写并启用实际要使用的供应商。仅 DDG 时需先验证网络可达和挑战页发生率；有自建 SearXNG 时，容器内地址不能用宿主机 localhost。
- `SEARCHAN_DOMAIN=bot.example.org`，只填域名。

生成 Webhook secret：

```sh
openssl rand -hex 32
```

把结果写入 `.env`，不要放进 URL、聊天或代码。服务要求 32–256 个合法字符，强于 Telegram 的最小格式要求。QQ 首发不启用，保持 `QQ_APP_ID` 为空。

## 2. 构建与本地预检

开发机验证：

```sh
go install golang.org/x/vuln/cmd/govulncheck@v1.8.0
TYPST_BIN=/path/to/typst sh scripts/verify.sh
```

脚本必须通过竞态测试、vet、漏洞扫描、编译、离线回归和实际 Typst 渲染。缺少工具会失败，不会静默跳过。离线回归集是合成开发集，不能当作真实搜索质量成绩。

Linux 容器验证：

```sh
docker build --target test -t searchan:test .
docker compose build
docker compose run --rm searchan doctor -strict
```

`doctor -strict` 做本地配置、访问策略、模型配置、持久化写入和真实中文卡片渲染检查，失败返回非零。它不会调用搜索或模型，不验证真实密钥和答案正确性；运行中的服务也可单独执行，不竞争任务目录锁。

镜像构建还会真实渲染示例，检查 Typst 可执行文件和中文字体。CI 同时验证运行镜像的非 root、只读根文件系统预检、生产 Compose 配置和 Caddyfile。发布前在有 Docker 的环境取得成功记录。`caddy:2-alpine` 和系统基础镜像会随重建更新；正式发布时记录镜像 digest，并使用同一构建产物回滚。

## 3. 启动 HTTPS 与注册 Webhook

```sh
docker compose -f compose.yaml -f compose.production.yaml up -d
docker compose exec searchan healthcheck
docker compose exec searchan telegram info
docker compose exec searchan telegram webhook -url https://bot.example.org/webhooks/telegram
docker compose exec searchan doctor -strict -online
```

`telegram info` 验证 Bot 身份并读取 Webhook 状态。核对其 username 与配置相同。注册只订阅 `message`，并限制 Webhook 连接数量；默认保留待处理更新。只有确实要删除积压消息时才显式使用 `-drop-pending`。

`doctor -online` 只读取 Bot 身份和 Webhook 注册状态，不发送消息；这些检查仍不能证明公网回调已到达或图片交付成功。

在白名单内的 Telegram 测试会话执行：

```text
/start
/search Go context cancellation 如何使用，给出来源
/sources 返回的任务ID
/status 返回的任务ID
```

确认收到真实 PNG 与可点击来源，手机端文字清晰、引用对应原文。对其他 Bot 的 `/search@other_bot` 不响应；群内对本 Bot 的带用户名命令需配置正确的 username。

反向代理只公开 `POST /webhooks/telegram`。`/healthz`、`/readyz` 留在本机/容器网络，不向公网提供内部状态。Webhook secret 仍由 Go 服务验证。Caddy 默认不启用访问日志；如果自行启用，应过滤 Webhook secret 请求头和请求正文。

## 4. 发布验收

下列项目完成后才作为正式上线记录；真实消息测试只在你指定的测试会话执行。

| 检查 | 验收证据 |
|---|---|
| 代码 | verify 脚本通过，扫描工具及 Go 版本记录在案 |
| 容器 | Linux 测试阶段、运行镜像预检、生产 Compose 和 Caddyfile 均通过 |
| 凭据/网络 | Bot 身份正确，搜索/模型在部署主机真实可达，无认证错误 |
| 回调/交付 | 公网 Webhook 收到消息，测试会话收到 PNG 和来源 |
| 权限 | 非白名单用户不排队、不调用搜索；指定群/用户能正常使用 |
| 去重/取消 | 同一 update 不重复搜索/交付；取消停止执行并释放配额 |
| 故障恢复 | 渲染失败可发文字；明确 429 有限重试；未知交付结果不自动重发 |
| 停机/恢复 | 在途工作在宽限期内完成；未领取任务在重启后继续；已保存研究结果可继续渲染 |
| 搜索质量 | 中文事实、技术、比较、最新、核实、无答案等真实问题人工核对；记录来源支持、时效、错误和缺口 |
| 运维 | 日志轮转、磁盘告警、备份恢复和旧镜像回滚演练通过 |

质量题集至少先覆盖以上类型，保留人工判断和 `output.json`。开发样例 nDCG、代码覆盖率均不能替代事实正确率。不把供应商限流/网络失败包装为“没有答案”。有支持性核验失败或来源冲突的回答需显示其限制。

## 5. 运维与故障处理

`/healthz` 表示 HTTP 服务存活；`/readyz` 检查实际持久化写入并返回排队/运行数量，磁盘写入失败或正在停机时返回 503。写入探测最多缓存 5 秒。默认队列 32、worker 2，每用户最多 2 个未完成任务、每会话 4 个；普通命令和控制回复各有两秒频率限制。

搜索与模型有明确预算，平台只对明确 429 拒绝最多重试两次、每次等待不超过 5 秒。超时、5xx、缺失回执等结果不明的发送进入 `uncertain`。不要通过编辑文件或批量重启来重发这类记录：先核对平台是否已收到。日志记录任务 ID、阶段统计和交付状态，不记录密钥、问题全文或正文。

默认 SIGTERM 宽限 30 秒，容器停止宽限 45 秒。停机先拒绝新 Webhook，再等待在途任务；到期取消剩余执行。未知的付费搜索不会在重启时自动重复；已落盘的研究结果可恢复后续处理。不要对同一数据目录启动多个实例，不要执行 `docker compose down -v` 删除数据卷。

```sh
docker compose logs --tail 100 searchan
docker compose exec searchan healthcheck
docker compose exec searchan version
docker compose stop searchan
```

日志默认最多 3 × 10 MiB。结束任务保留 7 天，网页缓存默认 30 分钟；`uncertain` 保留待核对。TTL 不是磁盘容量上限，需监控任务卷、备份目录和剩余磁盘空间。

## 6. 备份与回滚

一致性备份在停止 Searchan 后进行，不复制正在写入的目录。默认 Compose 项目名固定为 searchan，任务卷为 searchan_searchan-data。

```sh
docker compose stop searchan
mkdir -p backups
docker run --rm -v searchan_searchan-data:/data:ro -v "$PWD/backups:/backup" debian:bookworm-slim tar -C /data -czf /backup/searchan-data.tar.gz .
docker compose start searchan
```

备份含用户问题、证据和平台标识，按敏感数据限制权限并另行加密保存；反复备份时用不同文件名。恢复前停止服务，先保留当前卷副本，再恢复到新卷或已清空的测试卷验证。不要直接覆盖运行中的数据卷。

回滚使用此前保存的镜像 digest 和匹配配置，保留数据卷；本轮没有引入数据库迁移。回滚前备份，回滚后检查 readiness、Webhook 和一个指定测试会话。数据记录不能被旧程序读取时，先在副本恢复验证，不能删除原始任务解决。

## 7. 不使用 Docker

`deploy/searchan.service` 提供 systemd 示例。安装服务用户、Go 编译产物到 `/opt/searchan/bin/searchan`、Typst 到 PATH、Noto CJK 字体以及仅服务用户可读的 `/etc/searchan.env`。配置监听 `127.0.0.1:8090`，前面仍需自己的 HTTPS 反向代理。该示例的权限和资源限制需在目标 Linux 主机验收；本机 macOS 不运行 systemd。

协议参考：[Telegram Webhook](https://core.telegram.org/bots/api#setwebhook)、[Webhook 状态](https://core.telegram.org/bots/api#getwebhookinfo)、[限流响应参数](https://core.telegram.org/bots/api#responseparameters)、[Typst 官方镜像构建](https://github.com/typst/typst/blob/main/Dockerfile)。
