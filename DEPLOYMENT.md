# Telegram 首发：Linux 原生部署

2026-10-10。默认使用 systemd，单机单进程，无需 Docker、Redis 或数据库。当前尚未提供服务器、域名和真实凭据；本地验收完成不代表已经上线。

## 准备发布包

目标环境为运行 systemd 的 Linux amd64 / arm64，推荐 Debian 12+ / Ubuntu 24.04+。需要 HTTPS 域名、Telegram Bot 凭据、模型接口及所选搜索后端，网络须可访问这些服务。80/443 对公网开放，Searchan 默认仅监听 `127.0.0.1:8090`。

正式产物由 [release Actions](https://github.com/YoisakiKnd/searchan/actions/workflows/release.yml) 构建，并上传到 [GitHub Releases](https://github.com/YoisakiKnd/searchan/releases)。推送 `vMAJOR.MINOR.PATCH` 或 `vMAJOR.MINOR.PATCH-rc.N` tag 后，工作流先验证该 tag 对应的提交，再运行 race、vet、漏洞扫描、真实 Typst 和 Linux systemd 安装/升级/回滚检查，全部成功后生成两个架构的安装包及 SHA-256。`-rc.N` 等带后缀版本自动标记 prerelease，不设为 latest。

维护者发布候选版本的命令如下；版本号需选择尚未使用的 tag：

```sh
git tag -a v0.1.0-rc.1 -m 'Searchan 0.1.0-rc.1'
git push origin v0.1.0-rc.1
```

普通 main / 分支提交的编译产物保存在 check 工作流的 Actions artifact，名称 linux-bundles，不自动发布正式版本。正式 Release 包的程序版本、提交号与包内 `BUILD_INFO.json` 一致，只有 publish job 有 contents:write 权限；使用内置 GITHUB_TOKEN，无需额外 PAT 或业务凭据。手动重试可在 release 工作流选择已有 tag，或执行 `gh workflow run release.yml --ref v0.1.0-rc.1`；不接受分支作为正式发布来源，也不覆盖已存在的 Release。创建/上传中途失败若留下草稿，先核对并清理失败草稿，再重试，保留原 tag。

也可在开发机使用 Go ≥1.26 生成同样结构的本地包：

```sh
bash scripts/package-linux.sh 0.1.0-rc.1
```

下载或打包得到 tar.gz 和 `SHA256SUMS-0.1.0-rc.1`，本地产物位于 dist/。传到服务器后核对校验和，选择匹配 CPU 的包解压。安装脚本需要包内的 service、管理脚本及配置示例，不要单独复制。版本目录同时保存程序与 Typst，便于一起回滚。安装包不附带 Linux Typst。

从 [Typst 官方 v0.15.1 发布页](https://github.com/typst/typst/releases/tag/v0.15.1) 准备 `typst-x86_64-unknown-linux-musl.tar.xz` 或 `typst-aarch64-unknown-linux-musl.tar.xz`，从可信发布元数据取得 SHA-256。安装器只读取本地文件，不执行远端安装脚本。已有可信 Typst 时可以直接传 `--typst /path/to/typst`。

## 配置和安装

在解压目录复制 `.env.example` 为 `production.env`，权限设为 600，填写：

- `TELEGRAM_BOT_TOKEN`、`TELEGRAM_BOT_USERNAME`，username 不带 `@`。
- `TELEGRAM_WEBHOOK_SECRET`，通过 `openssl rand -hex 32` 生成。要求 32–256 个字母、数字、下划线或连字符，不写进 URL。
- `SEARCHAN_ALLOWED_USERS=telegram:你的数字用户ID` 或 `SEARCHAN_ALLOWED_CHATS=telegram:群ID`，用户与会话按“或”授权；默认不公开服务。
- `MODEL_ENDPOINT`、`MODEL_NAME`、所需 `MODEL_API_KEY`，保留 `SEARCHAN_MODEL_VERIFY=true`。
- `SEARCHAN_PROVIDERS` 及对应凭据，仅启用实际使用的后端。DDG 无密钥，但需要验证部署网络的可达性及挑战页发生率。

保留 `SEARCHAN_LISTEN=127.0.0.1:8090` 和 `TYPST_BIN=typst`，由服务 PATH 使用当前版本的 Typst；QQ_APP_ID 留空。SEARCHAN_DOMAIN 仅用于可选 Compose，原生 Caddy 示例直接填写域名。环境文件使用 `NAME=value`，不写 export、变量插值或 shell 命令。安装器不会 source 或打印该文件。

在解压目录执行，替换 Typst 文件和摘要：

```sh
sudo bash scripts/install-linux.sh \
  --typst-archive /path/to/typst-x86_64-unknown-linux-musl.tar.xz \
  --typst-sha256 EXPECTED_SHA256 \
  --env-file ./production.env --install-deps --start
```

`--install-deps` 仅在显式指定时通过 apt 安装 CA 证书及 Noto CJK 字体；其他发行版自行安装后去掉此参数。已有 Typst 时，用 `--typst /usr/local/bin/typst` 替换 archive/sha 参数。源码目录也支持 `--from-source --version 0.1.0-rc.1`；服务器无需永久保留 Go。

| 路径 | 用途 |
|---|---|
| `/opt/searchan/releases/<版本>/` | 程序与 Typst，已有版本不允许被不同内容覆盖 |
| `/opt/searchan/current` | 原子切换的当前版本链接 |
| `/opt/searchan/bin/` | 稳定命令入口及 searchan-admin |
| `/etc/searchan.env` | root:searchan，0640 |
| `/var/lib/searchan` | searchan 账号私有数据，0700 |
| `/etc/systemd/system/searchan.service` | 权限、资源限制和优雅停机配置 |

脚本创建独立 searchan 系统用户。启动前执行 doctor -strict，验证访问策略、模型配置、持久写入、中文字体和实际 PNG；启动后检查 readiness。升级或启动失败会尝试恢复旧版本、旧配置、旧 service 及原运行状态，故障详情查看 journal。

首次不传 --start 时只安装，方便后续编辑配置；原服务在运行时升级默认重新启动，--no-start 则明确保持停止。已有配置默认保留；再次传 --env-file 必须显式配合 --replace-env。数据不会被升级或回滚删除。安装使用锁阻止并发切换。

```sh
sudo systemctl status searchan
sudo /opt/searchan/bin/searchan-admin healthcheck
sudo /opt/searchan/bin/searchan-admin version
sudo journalctl -u searchan -n 100
```

searchan-admin 通过 systemd 临时单元，以服务账号和相同环境执行管理命令，不通过 shell 加载密钥。严格预检只验证本地，不调用付费搜索或模型，不证明真实密钥有效。AI 总结为必需功能，普通 doctor 也会拒绝缺少模型配置；仅有搜索摘要或核验遇到问题时返回带限制说明的短总结，生成及引用校验失败会明确报错。

模型支持 OpenAI Chat Completions / Responses、Anthropic Messages 及兼容网关；协议识别、认证和思考配置见 [README 模型说明](README.md)。必须填写完整 URL。自定义路径需要显式设置 `MODEL_API`，使用 Bearer 认证的 Anthropic 网关另设 `MODEL_AUTH=bearer`。新参数需升级到包含该功能的版本，旧安装包不会自动获得代码更新。

若小请求成功、实际总结报连接重置，先用服务的相同账号、环境和较大输入检查模型通路：

```sh
sudo /opt/searchan/bin/searchan-admin model check -timeout 30s
```

该命令主动调用模型，默认允许一次传输重试，可能计费；不发搜索请求或 Telegram 消息。它会发送约 10 KB 合成资料并要求 JSON，成功后输出耗时。连接重置本身无法证明是思考参数、超时或供应商故障；查看 journal 中协议、尝试次数、输入大小和耗时，再与服务商日志核对。升级后的独立模型连接池不再受搜索连接池的 15 秒响应头上限影响，仍受任务和阶段总预算约束。

默认规划 4 秒、模型阶段 18 秒、研究 45 秒、任务 90 秒。只提高 `SEARCHAN_MODEL_TIMEOUT` 不一定增加实际可用时间，因为研究剩余额度会继续裁剪阶段 deadline。先用自检测量服务耗时，再一起调整模型与研究时间，并为 Typst 和发送留出任务额度。官方 DeepSeek 默认关闭思考；其他端点默认不附加专用思考字段，按模型文档明确配置。

若机器人提示“AI 总结未通过格式或引用校验”，查看任务文件的 `error` 或执行 `sudo journalctl -u searchan --since "15 minutes ago" --no-pager`。旧版本的 `brief summary exceeds single-paragraph or 300-character limit` 合并了超字数、非空章节和表格三种情况，不能仅凭它判断是哪一种。新版本使用独立短总结提示词并为程序的限制说明预留字符；布局失败时可在原预算内修正一次，仍失败则日志会给出 `total_chars`、`max_chars`、`sections`、`has_table`。引用缺失 / 不存在的 ID、非法 JSON 和生成失败不走布局修正；不要关闭引用校验来规避错误。

## HTTPS 和 Telegram 回调

使用现有反向代理，或参考 `deploy/Caddyfile.native`，把 bot.example.org 改为你的域名并按主机的 Caddy 安装方式配置。只公开 `POST /webhooks/telegram`，代理到 `127.0.0.1:8090`；health/readiness 不公开。Caddy 安装独立于 Searchan 安装器，避免覆盖已有网站配置。

HTTPS 就绪后：

```sh
sudo /opt/searchan/bin/searchan-admin telegram info
sudo /opt/searchan/bin/searchan-admin telegram webhook -url https://bot.example.org/webhooks/telegram
sudo /opt/searchan/bin/searchan-admin doctor -strict -online
```

核对 Bot username。注册只订阅 message，默认保留积压更新；只有明确需要丢弃时才加 -drop-pending。在线预检仅调用 getMe / getWebhookInfo，不发送消息，仍需在指定测试会话验证公网回调和图片交付。

```text
/start
/search Go context cancellation 如何使用，给出来源
/status 返回的任务ID
/sources 返回的任务ID
```

确认手机收到清晰 PNG 与可点击来源，引用对应原文；非白名单用户不能产生付费任务。群内 `/search@本Bot` 需正确 username；其他 Bot 命令忽略。自行启用代理访问日志时过滤 secret 请求头和正文。

## 升级、回滚及备份

升级前备份，用新版本发布包再次运行安装器。回滚在保留安装脚本的包目录执行，无需 Go 或重新提供 Typst：

```sh
sudo bash scripts/install-linux.sh --rollback 0.1.0-rc.1 --start
```

成功回滚保留当前配置和数据。配置也需回退时，提供对应备份并显式 --env-file ... --replace-env。旧程序和新数据格式兼容性需在副本验证，不能删除原任务来处理兼容错误；当前版本没有数据库迁移。

一致性备份先停止服务，每次使用新文件名：

```sh
sudo systemctl stop searchan
sudo install -d -m 0700 /var/backups/searchan
sudo tar -C /var/lib -czf /var/backups/searchan/data-20261010.tar.gz searchan
sudo chmod 0600 /var/backups/searchan/data-20261010.tar.gz
sudo cp -p /etc/searchan.env /var/backups/searchan/config-20261010.env
sudo systemctl start searchan
```

备份含用户问题、证据和平台标识，应限制权限并另行加密。恢复时停止服务、保留当前副本，在临时位置解压验证后恢复目录及 searchan 所有权；不要覆盖运行中的数据。恢复后检查 readiness、Webhook 和指定测试会话。

## 资源和运行行为

systemd 设置整体 MemoryMax=384M（含 Typst）、CPU 最多两核、TasksMax 96；Go GC 96 MiB 为软预算。默认队列 32、worker 2，每用户 2 / 会话 4 个未完成任务，搜索/读取并发各 3，Typst 并发 1。根据目标主机实测调整，尚无生产 P95 或峰值 RSS 结论。

/healthz 表示 HTTP 存活，/readyz 实际探测磁盘写入，报告排队/运行数，写入失败或停机返回 503，最多缓存 5 秒。SIGTERM 停止接收新任务，给在途工作默认 30 秒；systemd 停机宽限 45 秒。一个数据目录只运行一个实例。

明确 429 拒绝最多重试两次、每次等待≤5 秒；超时、5xx、缺失回执等不明发送标记 uncertain，避免自动重复交付。重启恢复排队任务及已保存的研究结果；未保存结果的运行任务失败，发送中记录 uncertain。核对平台是否已收到，再决定重新提问。

日志使用 journald，不打印密钥和问题全文，按主机策略设置日志容量。结束任务保留 7 天、网页缓存默认 30 分钟，uncertain 留待核对。TTL 不是磁盘容量上限，需要监控及清理策略。

## 发布检查与待验收

开发机统一检查，缺少工具会失败，不会静默跳过：

```sh
go install golang.org/x/vuln/cmd/govulncheck@v1.8.0
TYPST_BIN=/path/to/typst sh scripts/verify.sh
```

包含 race、vet、漏洞扫描、安装器暂存测试、离线回归和实际渲染。暂存测试覆盖首次安装、重复安装、升级保留配置、回滚、版本保护、切换失败恢复，以及不执行环境文件。macOS 暂存测试不能证明 Linux systemd 实机启动成功。

| 上线前检查 | 所需证据 |
|---|---|
| Linux 主机 | systemd 启动、资源限制、readiness、升级/回滚和备份恢复演练 |
| 真实接口 | Bot 身份正确；搜索和模型可达、鉴权成功 |
| 公网交付 | 指定会话收到 PNG 与来源，手机端文字清晰 |
| 权限和恢复 | 白名单、重复 update、取消、限流、重启、不明交付符合预期 |
| 搜索质量 | 事实、技术、比较、时效、核实、无答案题目人工核对来源支持与缺口 |
| 运维 | 日志容量、磁盘告警、敏感备份和恢复流程可用 |

内置 10 题是合成开发回归集，不能代替真实搜索质量、事实正确率或引用支持率。服务器及凭据准备后，再执行真实验收；当前没有上线成功或“搜索效果最好”的实测结论。

Docker 仍可选，见源码的 `deploy/DOCKER.md`，不是原生部署或发布前提。协议参考：[Telegram Webhook](https://core.telegram.org/bots/api#setwebhook)、[响应参数](https://core.telegram.org/bots/api#responseparameters)。
