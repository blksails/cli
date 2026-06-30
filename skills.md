# bk 使用手册（skills.md）

> `bk` 是黑帆云（BlackSails Cloud）的统一命令行工具：管理 Dokku 应用、发布与配置应用、本地代理（HTTP 镜像 / TCP 转发）、SSH 密钥发放与加密 Secret Vault。
>
> 本文是**面向使用者的速查手册**，按「先上手、再分场景、最后查命令」组织。完整的安装/发布说明见 [`README.md`](README.md)，配置细节见 [`docs/config.md`](docs/config.md)。

---

## 目录

- [1. 5 分钟上手](#1-5-分钟上手)
- [2. 核心概念](#2-核心概念)
- [3. 命令族速查](#3-命令族速查)
- [4. 按场景使用](#4-按场景使用)
  - [4.1 新用户初始化](#41-新用户初始化)
  - [4.2 管理 Dokku 应用](#42-管理-dokku-应用)
  - [4.3 应用环境变量](#43-应用环境变量)
  - [4.4 Secret Vault（加密密钥）](#44-secret-vault加密密钥)
  - [4.5 本地代理：TCP 转发 / HTTP 镜像](#45-本地代理tcp-转发--http-镜像)
  - [4.6 SSH 密钥发放](#46-ssh-密钥发放)
- [5. 配置与多环境（profile）](#5-配置与多环境profile)
- [6. 排错与自检](#6-排错与自检)
- [7. 完整命令参考](#7-完整命令参考)

---

## 1. 5 分钟上手

```bash
# 1) 安装后确认版本（安装方式见 README）
bk version

# 2) 登录（零配置：Supabase 端点/密钥已内置）
bk auth login -u you@example.com         # 或交互式输入密码

# 3) 一键初始化新环境：同步主机目录 + 写首次连接配置 + 生成并登记 SSH 密钥
bk init

# 4) 环境自检，确认配置/登录态/SSH 可达
bk doctor

# 5) 开始用：列举应用
bk app ls
```

> 之后请联系管理员执行 `bk ssh-key install` 把你的公钥代装到 Dokku，才能 `git push` 部署。

---

## 2. 核心概念

| 概念 | 说明 |
| --- | --- |
| **profile（配置档）** | 一套独立的登录态 + 连接目标，用全局 `--profile <name>` 切换（默认 `default`）。不同 profile 可指向不同 Dokku 主机/环境。 |
| **主机目录（host）** | 登录后自动从线上同步的 Dokku 主机坐标，`bk app`/`bk proxy` 会自动取用，无需手填 host。 |
| **proxy hub** | NAT 友好的隧道汇聚点（yamux+TLS）。`mirror`/`forward` 客户端拨入 hub。「登录即用」，坐标来自在线目录。 |
| **Vault** | 本机主密钥加密 secret，密文存 Supabase，按登录身份多端共享。主密钥仅在本机 `~/.local/bk/vault.key`，永不上传。 |
| **allowlist** | proxy 转发目标白名单，中心化于 Supabase，hub 侧防 SSRF 的安全控制。 |

**配置优先级**：`命令行 flag > 环境变量 > 配置文件（.bs.yaml）`

---

## 3. 命令族速查

| 命令 | 用途 |
| --- | --- |
| `bk auth` | 登录 / 登出 / 查看当前身份 / 列出配置档 |
| `bk init` | 一键初始化新用户环境 |
| `bk app` | 管理 Dokku 应用：列举/创建/销毁/配置/进程/日志/重启/扩缩容 |
| `bk vault` | Secret Vault：本机加密存储、Supabase 共享 |
| `bk proxy` | 本地代理：HTTP 流量镜像（mirror）/ TCP 端口转发（forward）/ hub / allowlist |
| `bk ssh-key` | SSH 密钥发放：生成 / 登记 / 代装 / 吊销 |
| `bk host` | Dokku 主机目录（登录后自动同步） |
| `bk doctor` | 环境自检（配置 / 登录态 / SSH 可达性） |
| `bk update` | 自升级到最新版（别名 `upgrade`） |
| `bk version` | 显示版本与构建信息 |

> 任何命令加 `--help` 都能看到详细说明与示例，例如 `bk app logs --help`。

---

## 4. 按场景使用

### 4.1 新用户初始化

```bash
bk auth login -u you@example.com   # 先登录
bk init                            # 同步目录 + 首次连接配置 + 生成并登记 SSH 密钥
```

`bk init` 完成后：

- `bk app ls`、`bk proxy forward` 登录即用（主机与 hub 坐标来自在线目录）；
- 你的公钥已登记为 **pending**，待管理员 `bk ssh-key install` 代装后即可 `git push` 部署。

不想自动生成 SSH 密钥时加 `--no-provision`。

---

### 4.2 管理 Dokku 应用

`bk app` 通过进程内 SSH 连接当前 profile 指向的 Dokku 主机。

```bash
bk app ls                       # 列出全部应用
bk app create myapp             # 创建应用
bk app ps myapp                 # 查看进程运行状态
bk app restart myapp            # 重启
bk app scale myapp web=3        # 扩缩容：web 进程 3 副本
bk app scale myapp worker=0     # 关停 worker

# 日志
bk app logs myapp               # 日志快照
bk app logs myapp -n 100        # 最近 100 行
bk app logs myapp -p web        # 仅 web 进程
bk app logs myapp -t            # 持续流式（Ctrl-C 退出）
bk app logs myapp -q -n 50 -p worker   # 原始日志（去颜色/时间戳）

# 销毁（高危，默认二次确认）
bk app destroy myapp            # 交互确认
bk app destroy myapp --force    # 跳过确认（脚本用）
```

**常用 flag**：

- `--raw`：支持的子命令直接输出 dokku 原始文本而非表格。
- `--sudo`：以 `sudo dokku` 形式执行（普通管理员账号使用）。

---

### 4.3 应用环境变量

```bash
bk app config myapp                          # 查看全部环境变量（表格）
bk app config myapp --raw                    # dokku 原始输出

bk app config:set myapp KEY=value            # 设置单个
bk app config:set myapp A=1 B=2 --no-restart # 批量设置且不触发重启

bk app config:unset myapp KEY                # 删除一个或多个
```

> 注意区分：`bk app config:*` 是 **Dokku 应用的明文环境变量**；`bk vault` 是 **加密 secret**（见下）。敏感凭据建议放 Vault。

---

### 4.4 Secret Vault（加密密钥）

以本机主密钥加密 VALUE，密文存入 Supabase，按登录身份（`--profile`）多端共享。

```bash
bk vault set myapp KEY=VALUE                 # 加密并写入（upsert），可一次多个
bk vault set myapp A=1 B=2
bk vault get myapp KEY                        # 取回并解密单个，仅输出明文
bk vault list myapp                           # 列出 key 名（不显示值）
bk vault rm myapp KEY                          # 删除单个
bk vault export myapp                          # 全部解密为 KEY=VALUE env 格式输出
```

把 Vault 的 secret 灌进应用配置：

```bash
bk vault export myapp | xargs bk app config:set myapp
```

> 主密钥仅存于本机 `~/.local/bk/vault.key`，**不要丢失**：丢失后已存的密文将无法解密。

---

### 4.5 本地代理：TCP 转发 / HTTP 镜像

两种模式共享同一套 yamux+TLS 隧道连接配置（客户端拨入 Hub，NAT 友好）。Hub 连接参数（`--server`/`--token`/`--app`/`--insecure`/`--ca`/`--server-name`）来自 `proxy` 父命令标志或 `.bs.yaml` 的 `proxy.*` 块——「登录即用」时无需手填。

#### TCP 端口转发（forward）

本地监听端口，把入站 TCP 连接经隧道或直连转发到远端目标。表达式形如 `local:host:remote` 或 `local:remote`（远端主机默认 `127.0.0.1`）。

```bash
# 本地 8080 → app:80，同时本地 9090 → 127.0.0.1:80
bk proxy forward 8080:app:80 9090:80

# 经隧道：本地 8080 → app.internal:80
bk proxy forward --server hub:8443 --token <tok> --app demo 8080:app.internal:80

# 同网段直连（不建隧道），本地 5432 → db.local:5432
bk proxy forward --direct 5432:db.local:5432
```

#### HTTP 流量镜像（mirror）

作为 Consumer 拨入 Hub，把 Hub 镜像下来的 HTTP 请求**反代到本地** target。单向，响应不回送线上，用于在开发机调试线上请求副本。

```bash
# 把 GET /api 前缀的镜像请求反代到本地 8080
bk proxy mirror --server hub:8443 --token <tok> --app demo \
  --target http://127.0.0.1:8080 --method GET --path /api

# 按请求头过滤（可重复，全部需匹配）
bk proxy mirror --target http://127.0.0.1:8080 \
  --header X-Env:dev --header X-Team:core
```

常用 mirror flag：`--target`(必填) `--method` `--path` `--host` `--header K:V` `--rule-id`。

#### hub / allowlist 管理

```bash
bk proxy hub ls                              # 查看缓存的 proxy hub 目录

bk proxy target ls                           # 查看转发目标白名单（任意已认证用户）
bk proxy target add demo db.local:5432 --note "..."   # 管理员：新增
bk proxy target rm  db.local:5432            # 管理员：移除
bk proxy target sync                         # 管理员：渲染进 hub config 并重启 hub 生效
```

> allowlist 是 hub 侧防 SSRF 的安全控制：仅清单内目标可经隧道转发。`target add/rm` 只改 Supabase 真源，**需 `sync` 后 hub 才生效**（hub 无热重载，sync 会重启）。

---

### 4.6 SSH 密钥发放

```bash
# 普通用户
bk ssh-key provision                  # 本机生成密钥对并登记公钥（pending）
bk ssh-key provision --set-identity   # 顺手把 .bs.yaml 的 ssh.identity 指向新私钥
bk ssh-key list                       # 查看自己登记的密钥与状态

# 管理员
bk ssh-key install                    # 仅列出全部 pending 供审核（不代装）
bk ssh-key install bk-alice-... --sudo  # 代装指定名称/指纹
bk ssh-key install --all --sudo        # 代装全部 pending
bk ssh-key revoke ...                  # 吊销并从 Dokku 移除
```

> 私钥以 `0600` 落盘且**永不离开本机**，仅上传公钥/指纹/名称。状态流转：`pending`（已登记）→ 管理员 `install` → `installed`（可用）。

---

## 5. 配置与多环境（profile）

`bk` 通过「配置文件 + 环境变量 + 命令行 flag」三层管理配置，优先级 `flag > env > 文件`。

- 默认配置文件：主目录与当前目录查找 `.bs.yaml`；用 `--config <path>` 指定。
- 用 `--profile <name>` 切换配置档（默认 `default`），可指向不同主机/环境。

```bash
bk auth list                       # 列出本机所有配置档
bk app ls --profile production      # 针对 production 档操作
bk doctor --profile production      # 自检指定档
```

常用全局 flag：

| Flag | 说明 | 默认 |
| --- | --- | --- |
| `--config` | 配置文件路径 | 自动查找 `.bs.yaml` |
| `--api-endpoint` | API 端点 | `https://supabase.blksails.cn` |
| `--api-key` | API 密钥（Supabase anon key） | 内置生产 anon key |
| `--profile` | 配置档名称 | `default` |

更多细节见 [`docs/config.md`](docs/config.md) 与 [`docs/ssh-keys.md`](docs/ssh-keys.md)、[`docs/proxy-targets.md`](docs/proxy-targets.md)。

---

## 6. 排错与自检

```bash
bk doctor          # 检查：.bs.yaml 可解析 / 当前 profile 登录态 / SSH 主机可达性
bk auth whoami     # 查看当前身份与会话状态
```

`bk doctor` 每项失败都给出可执行的修复建议；全部关键检查通过时零退出码，存在关键失败时非零退出码（便于脚本判定）。输出**不含** access/refresh token 等敏感字段。

常见问题：

| 现象 | 处理 |
| --- | --- |
| 命令提示未登录 / 会话失效 | `bk auth login` 重新登录，再 `bk auth whoami` 确认 |
| `bk app ls` 连不上主机 | `bk doctor` 看 SSH 可达性；确认 `bk init` 已同步主机目录，公钥已被管理员 `install` |
| `git push` 部署被拒 | 你的公钥可能还是 `pending`，联系管理员 `bk ssh-key install` |
| proxy forward 目标被拒 | 目标不在 allowlist，需管理员 `bk proxy target add` + `sync` |
| 想升级 | `bk update --check` 查新版，`bk update` 升级 |

---

## 7. 完整命令参考

```
bk
├── auth                用户认证管理
│   ├── login           用户登录            (-u/--username, -p/--password)
│   ├── logout          用户登出
│   ├── whoami          查看当前身份与会话状态
│   └── list            列出认证配置档
├── init                一键初始化新用户环境   (--no-provision)
├── app                 管理 Dokku 应用       (--raw, --sudo)
│   ├── ls              列出全部应用
│   ├── create <app>    创建应用
│   ├── destroy <app>   销毁应用            (--force)
│   ├── ps <app>        查看进程状态
│   ├── restart <app>   重启
│   ├── scale <app> <process=count>   扩缩容
│   ├── logs <app>      日志              (-n/--num, -p/--ps, -q/--quiet, -t/--tail)
│   ├── config <app>    查看环境变量
│   ├── config:set <app> KEY=VALUE...     设置        (--no-restart)
│   └── config:unset <app> KEY...         删除
├── vault               Secret Vault
│   ├── set <app> KEY=VALUE...     加密写入（upsert）
│   ├── get <app> KEY              解密取回（仅输出明文）
│   ├── list <app>                列出 key 名
│   ├── rm <app> KEY               删除
│   └── export <app>              全部解密为 env 格式
├── proxy               本地代理              (--server, --token, --app, --insecure, --ca, --server-name)
│   ├── forward <expr>...   TCP 端口转发      (--direct)
│   ├── mirror              HTTP 流量镜像     (--target必填, --method, --path, --host, --header, --rule-id)
│   ├── hub ls             查看 hub 目录
│   └── target             allowlist 管理     (ls / add / rm / sync)
├── ssh-key             SSH 密钥发放          (--sudo)
│   ├── provision         生成并登记公钥(pending)  (--host, --email, --force, --set-identity)
│   ├── list              查看自己的密钥
│   ├── install [名称|指纹]...   (管理员)代装      (--all)
│   └── revoke            (管理员)吊销并移除
├── host ls             Dokku 主机目录        (--sync)
├── doctor              环境自检
├── update              自升级（别名 upgrade）  (--check, --force, -y/--yes, --version, --token)
└── version             版本与构建信息

全局 flag: --config  --api-endpoint  --api-key  --profile
```

> 一切以 `bk <command> --help` 的实时输出为准——每个命令都内置了中文说明与示例。
