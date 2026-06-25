# proxy 转发目标 allowlist（bk proxy target）

本文档说明如何用 `bk proxy target` 集中管理 yproxy hub 的转发目标 allowlist。

## 背景与原理

yproxy hub 通过 `forward_targets` allowlist 控制「哪些目标允许经隧道转发」——这是 hub 侧
**防 SSRF 的安全控制**：只有在 allowlist 内的目标才放行。

过去 allowlist 直接写在 hub 主机的 `config.yaml` 里，分散且需手工改文件 + 重启。
`bk proxy target` 把它**中心化到 Supabase**（`cli.proxy_targets` 表）作为唯一真源，
再由 `sync` 单向渲染进 hub 的 `config.yaml` 并重启 hub 使其生效。

```
        管理员                         任意已认证用户
  add / rm（写 Supabase）                ls（读 Supabase）
          │                                   │
          ▼                                   ▼
   ┌──────────────────────────────────────────────┐
   │      Supabase  cli.proxy_targets（真源）        │
   └──────────────────────────────────────────────┘
          │  sync（管理员，单向覆盖）
          ▼
   hub config.yaml 的 forward_targets ──► dokku restart ──► 生效
```

> **真源是 Supabase，不是 hub config.yaml。** `add`/`rm` 只改 Supabase，必须 `sync` 后 hub 才生效
> （hub 无热重载，sync 会重启 hub）。

## 数据模型

`cli.proxy_targets`（`cli` schema，复用 `cli.is_admin()`）：

| 列 | 说明 |
| --- | --- |
| `id` | uuid 主键 |
| `app_id` | yproxy app id（如 `infra`），转发目标按它归组 |
| `target` | allowlist 模式：精确 `host:port`、`host:*`、`*:port`、`*`（与 yamuxproxy 一致） |
| `note` | 备注（可空） |
| `created_by` / `created_at` | 审计字段 |

约束：`unique(app_id, target)` 防重复；不存任何私钥/密钥类敏感信息。

**RLS：** 任意已认证用户可 `SELECT`；仅管理员（`cli.is_admin()`）可 `INSERT/UPDATE/DELETE`——与
「allowlist 是安全控制」相称。

## 命令

| 命令 | 角色 | 作用 |
| --- | --- | --- |
| `bk proxy target ls [--app <id>]` | 任意已认证 | 列出转发目标（可按 app 过滤） |
| `bk proxy target add <app> <host:port> [--note ...]` | 管理员 | 新增一个转发目标 |
| `bk proxy target rm <host:port\|id> [--app <id>]` | 管理员 | 移除一个转发目标 |
| `bk proxy target sync` | 管理员 | 把目标渲染进 hub config.yaml 并重启 hub 生效 |

### 查看 / 维护（改 Supabase 真源）

```bash
# 任意已登录用户：查看放行清单
bk proxy target ls
bk proxy target ls --app infra            # 只看某 app

# 管理员：增删（仅改 Supabase，需 sync 才生效）
bk proxy target add infra 10.0.0.5:5432 --note "内网 PG"
bk proxy target add infra "10.0.0.0/24:*"        # 模式示例
bk proxy target rm 10.0.0.5:5432 --app infra     # --app 避免误删同名 target
bk proxy target rm <id>                          # 也可按记录 id 删
```

`add`/`rm` 成功后会提示「运行 bk proxy target sync 使 hub 生效」。

### 同步到 hub（管理员，需 root 接入 hub 主机）

```bash
bk proxy target sync --config ~/.bs.admin.yaml
```

`sync` 流程：

1. 读 Supabase 全部目标，按 `app_id` 分组。
2. 经 `SSHConfig(profile)` 连接 hub 主机（**需 root**：要读写 `/var/lib/dokku/...` 并执行 `dokku`）。
3. 读取现有 `config.yaml`。
4. 渲染：**仅替换各 app 的 `forward_targets`，保留 mirror app 与其它所有字段**。
   - 若某 app 在 Supabase 有目标但 hub config.yaml 里没有该 app，会 `⚠` 警告并跳过
     （需先在 hub 配置里建该 forwarder app）。
5. 无变更 → 不写入、不重启。
6. 有变更 → 先备份（同目录 `.bak-sync`）→ 写入 → `dokku <plugin>:restart <service>` 重启生效。

### sync 相关配置键（`.bs.yaml` 的 `proxy.*`，均有默认）

| 键 | 默认 | 说明 |
| --- | --- | --- |
| `proxy.hub_plugin` | `proxyhub` | hub 的 dokku 插件名 |
| `proxy.hub_service` | `proxyhub1` | hub 实例名 |
| `proxy.hub_config_path` | 由 plugin/service 推导 | 直接指定 config.yaml 路径，缺省为 `/var/lib/dokku/services/<plugin>/<service>/config.yaml` |

> sync 需要 **root** 接入 hub 主机，建议单独用一份 `user: root` 的管理员配置（如 `~/.bs.admin.yaml`）
> 经 `--config` 指定，与日常 `user: dokku` 的配置区分开。

## 安全要点

- allowlist 是 hub 侧防 SSRF 的安全控制，**只有 allowlist 内的目标可经隧道转发**。
- 本组命令只搬运 `(app_id, target)` 文本，不放行任何 hub 配置以外的目标。
- 写权限受 RLS 限定为管理员；普通用户只读，无法扩大放行面。
- sync 写入前备份 config.yaml，重启失败时备份可手动恢复。

## 启用前提

需先把 `migrations/proxy_targets.sql` 应用到生产 Supabase，并确保 `cli` schema 已暴露（PGRST）：

```bash
psql "$PROD_DB_URL" -v ON_ERROR_STOP=1 -f migrations/proxy_targets.sql
```

## 相关文档

- [config.md](config.md) — `.bs.yaml` 配置（含 `ssh:` 块与 `proxy.*`）
- [ssh-keys.md](ssh-keys.md) — SSH 密钥发放
