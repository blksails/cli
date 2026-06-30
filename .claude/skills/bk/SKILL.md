---
name: bk
description: 用黑帆云 bk CLI 管理 Dokku 应用、应用配置、Secret Vault、本地代理（HTTP 镜像 / TCP 转发）与 SSH 密钥发放。当用户提到 bk、黑帆云、BlackSails Cloud，或要「列举/创建/销毁应用」「看应用日志/进程」「设应用环境变量」「存取加密 secret / vault」「端口转发 / 流量镜像 / proxy」「发 SSH 密钥」「登录黑帆云」「初始化 bk 环境」时触发。
allowed-tools: Bash, Read
---

# bk Skill —— 黑帆云命令行助手

帮助使用 `bk`（BlackSails Cloud CLI）完成应用管理、配置、密钥与代理等操作。

## 使用原则

- **以实时帮助为准**：任何命令的精确用法、flag 与示例，用 `bk <command> --help` 现查，不要凭记忆编造。
- **高危操作先确认**：`bk app destroy`（销毁应用）、`bk vault rm`、`bk ssh-key revoke`、`bk proxy target sync`（会重启 hub）等不可逆/影响面大的操作，执行前向用户确认；脚本化场景才用 `--force`/`-y`。
- **认准 profile**：所有操作都受全局 `--profile <name>` 影响（默认 `default`），指向不同主机/环境。不确定时先 `bk auth whoami` / `bk doctor`。
- **凭据要加密**：敏感值用 `bk vault`（加密，存 Supabase），而非 `bk app config:set`（明文环境变量）。
- 详细手册见仓库 `skills.md`（按场景的完整示例）。

## 快速上手

```bash
bk version                          # 确认已安装
bk auth login -u you@example.com    # 登录（端点/密钥已内置，零配置）
bk init                             # 一键初始化：同步主机目录 + 首次连接配置 + 生成并登记 SSH 密钥
bk doctor                           # 自检：配置 / 登录态 / SSH 可达性
bk app ls                           # 开始使用
```

## 命令速查

| 任务 | 命令 |
| --- | --- |
| 登录 / 身份 | `bk auth login -u <email>` · `bk auth whoami` · `bk auth logout` · `bk auth list` |
| 初始化新环境 | `bk init`（`--no-provision` 跳过建 SSH 密钥） |
| 列举 / 创建 / 销毁应用 | `bk app ls` · `bk app create <app>` · `bk app destroy <app>`（高危，默认二次确认） |
| 进程 / 重启 / 扩缩容 | `bk app ps <app>` · `bk app restart <app>` · `bk app scale <app> web=3` |
| 日志 | `bk app logs <app>`（`-n N` `-p web` `-t` 流式 `-q` 原始） |
| 应用环境变量（明文） | `bk app config <app>` · `bk app config:set <app> K=V [--no-restart]` · `bk app config:unset <app> K` |
| Secret Vault（加密） | `bk vault set <app> K=V` · `bk vault get <app> K` · `bk vault list <app>` · `bk vault rm <app> K` · `bk vault export <app>` |
| TCP 端口转发 | `bk proxy forward 8080:app:80 9090:80`（`--direct` 直连不建隧道） |
| HTTP 流量镜像 | `bk proxy mirror --target http://127.0.0.1:8080 [--method --path --host --header K:V]` |
| proxy hub / 白名单 | `bk proxy hub ls` · `bk proxy target ls`（管理员 `add`/`rm`/`sync`） |
| SSH 密钥 | `bk ssh-key provision` · `bk ssh-key list`（管理员 `install [--all]` / `revoke`） |
| 主机目录 | `bk host ls [--sync]` |
| 自检 / 升级 / 版本 | `bk doctor` · `bk update [--check]` · `bk version` |

全局 flag：`--config` `--api-endpoint` `--api-key` `--profile`（优先级：flag > 环境变量 > `.bs.yaml`）。

## 排错首选

1. 命令失败先跑 `bk doctor`（检查配置 / 登录态 / SSH 可达性，每项失败都给修复建议）。
2. 未登录/会话失效 → `bk auth login` 再 `bk auth whoami`。
3. `git push` 部署被拒 → 公钥可能仍是 `pending`，需管理员 `bk ssh-key install`。
4. `proxy forward` 目标被拒 → 不在 allowlist，需管理员 `bk proxy target add` + `sync`。
