# `.bs.yaml` 配置体系

`bk` CLI 通过 `.bs.yaml` 提供统一、稳定的配置结构，供各能力 spec（dokku-management、secret-vault、port-proxy 等）以一致的 key 命名读取配置，避免配置漂移。本文档列出全部配置键、默认值与示例，并与 `internal/config` 的映射实现保持一致。

## 概述

### 配置文件查找

- 未显式传入 `--config` 时，`bk` 按顺序在 **用户主目录** 与 **当前目录** 查找名为 `.bs.yaml` 的文件：
  1. `~/.bs.yaml`（先）
  2. `./.bs.yaml`（后）
- 也可用 `--config <path>` 显式指定配置文件路径，此时仅读取该文件。
- 来源：`cmd/root.go` 中 `configureConfigSources` 通过 `v.AddConfigPath(home)`、`v.AddConfigPath(".")`、`v.SetConfigName(".bs")`、`v.SetConfigType("yaml")` 装配查找路径。

### 优先级（标志 > 环境变量 > 配置文件）

同一配置项可能同时出现在命令行标志、环境变量与配置文件中，解析优先级为：

1. **命令行标志**（最高）—— 例如 `--api-endpoint`、`--api-key`；通过 `viper.BindPFlag` 绑定，显式设置的标志胜出。
2. **环境变量**（次之）—— `viper.AutomaticEnv()` 让匹配的环境变量覆盖配置文件值。
3. **配置文件**（最低）—— `.bs.yaml` 中的值。

来源：`cmd/root.go` 中 `initConfig` / `configureConfigSources` 的注释与实现（对应 Requirement 1.5）。

### 配置文件路径打印到 stderr

配置文件被成功加载后，正在使用的配置文件路径会打印到 **标准错误（stderr）**，而非标准输出（stdout），以免污染可被脚本消费的 stdout。

来源：`cmd/root.go` 中 `fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())`（对应 Requirement 1.6）。

### 容忍未知键

`.bs.yaml` 中无法识别的配置键 **不会** 中断命令执行。配置值通过 `viper.Get*` 按键读取，而非严格 `Unmarshal`，因此未知键被存入 viper 的底层 map 而不报错（对应 Requirement 2.8）。

## 配置键

### 顶层键

| 键 | 含义 | 默认值 | 说明 |
|----|------|--------|------|
| `api_endpoint` | Supabase / API 端点地址 | `https://supabase.blksails.cn` | 也可用 `--api-endpoint` 标志覆盖；标志默认值即此值（`cmd/root.go` 的 `StringVar(..., "api-endpoint", "https://supabase.blksails.cn", ...)`，绑定到 viper key `api_endpoint`）。 |
| `api_key` | API 密钥（Supabase anon / api key） | 无（空） | 也可用 `--api-key` 标志覆盖，绑定到 viper key `api_key`。**注意安全：** 该值是 Supabase anon/api key，属于敏感凭据；不要把含真实密钥的 `.bs.yaml` 提交到版本库或共享给他人。 |

来源：`cmd/root.go` 的标志定义与 `viper.BindPFlag("api_endpoint", ...)`、`viper.BindPFlag("api_key", ...)`，以及 `DefaultClient()` 中 `viper.GetString("api_endpoint")` / `viper.GetString("api_key")`。

### `ssh` 块

`ssh` 块是 **顶层、全局** 的连接配置（非 per-profile 子树），描述 Dokku 主机的 SSH 连接参数。其值由 `cmd/ssh_config.go` 通过 `viper.GetString("ssh.host")` 等读取，再交由 `internal/config` 的 `SSHSettings.ToSSHConfig()` 映射为 `internal/sshx.Config` 并补齐默认值。

| 键 | 含义 | 是否必填 | 默认值 | 映射目标（`sshx.Config`） |
|----|------|----------|--------|---------------------------|
| `ssh.host` | SSH 主机地址 | **必填** | 无 | `Host` |
| `ssh.user` | SSH 登录用户 | 可选 | 留空（**不** 默认为 `root`） | `User` |
| `ssh.port` | SSH 端口 | 可选 | `22` | `Port` |
| `ssh.identity` | 私钥文件路径 | 可选 | 无（空） | `IdentityFile` |
| `ssh.insecure` | 是否跳过主机密钥校验 | 可选 | `false` | `Insecure` |

逐键说明（与 `internal/config/ssh.go` 的 `SSHSettings.ToSSHConfig()` 实现一致）：

- **`ssh.host`（必填）**：SSH 目标主机地址。若缺失或仅含空白字符，`ToSSHConfig()` 返回明确错误 `config: 未配置 SSH 主机地址（ssh.host）`，而不构造无效配置。映射为 `Host`。
- **`ssh.user`（可选）**：SSH 登录用户。未配置时 **保持为空，并不会默认为 `root`**。空 `User` 由下游消费方应用领域默认值 —— `dokku.New` 会将空 user 默认为 `dokku`。映射为 `User`。
- **`ssh.port`（可选，默认 22）**：SSH 端口。未配置（值为 `0`）时 `ToSSHConfig()` 填入默认端口 `22`；显式值原样透传。映射为 `Port`。
- **`ssh.identity`（可选）**：SSH 私钥文件路径。原样透传，映射为 `IdentityFile`。
- **`ssh.insecure`（可选，默认 false）**：是否在生成 SSH 连接配置时跳过主机密钥校验。原样透传，映射为 `Insecure`。

#### `ssh.insecure=true` 的安全风险

- **`ssh.insecure=true`（默认 `false`）跳过 `known_hosts` 主机密钥校验。** 这会关闭对服务器身份的验证，使连接易受 **中间人攻击（MITM）**：攻击者可冒充目标主机截获或篡改流量。
- 仅应在 **受信任的内网或开发环境** 临时使用。
- 当 `ssh.insecure` 未设置或为 `false` 时，`bk` 保留主机密钥校验，依赖用户的 `~/.ssh/known_hosts`（对应 Requirement 2.6）。

来源：`internal/config/ssh.go`（默认值、必填校验、字段映射）、`cmd/ssh_config.go`（viper key 读取与边界说明）。

### `proxy` 块

`proxy` 块是 **顶层、全局** 的连接配置，**独立于 `ssh.*` 块**（两者互不依赖、互不混用），描述 port-proxy 能力中 yamux+TLS Hub 的连接参数（`bk proxy mirror` / `bk proxy forward` 共享同一套连接配置）。其值由 `cmd/proxy.go` 的 `resolveHubConfig`（核心 `resolveHubConfigFrom`）读取并校验：**命令行标志优先，否则回退 `.bs.yaml` 的 `proxy.*`**（与本文档「标志 > 环境变量 > 配置文件」的整体优先级一致）。

| 键 | 含义 | 是否必填 | 默认值 | 对应标志 |
|----|------|----------|--------|----------|
| `proxy.server` | Hub 的 TLS 地址 `host:port` | **必填**（mirror；forward 见下） | 无（空） | `--server` |
| `proxy.token` | 共享认证 token | **必填**（mirror；forward 见下） | 无（空） | `--token` |
| `proxy.app` | app_id | **必填**（mirror；forward 见下） | 无（空） | `--app` |
| `proxy.insecure` | 跳过 Hub TLS 证书校验（仅开发） | 可选 | `false` | `--insecure` |
| `proxy.ca` | 可选 CA bundle 路径 | 可选 | 无（空） | `--ca` |
| `proxy.server_name` | 可选 TLS ServerName 覆盖 | 可选 | 无（空） | `--server-name` |

逐键说明（与 `cmd/proxy.go` 的 `resolveHubConfigFrom` 实现一致）：

- **`proxy.server`（必填）**：Hub 的 TLS 监听地址，形如 `host:port`。标志 `--server` 优先，否则取 `proxy.server`。
- **`proxy.token`（必填）**：连接 Hub 的共享认证 token。标志 `--token` 优先，否则取 `proxy.token`。**敏感凭据**，切勿提交到版本库或共享给他人。
- **`proxy.app`（必填）**：app_id，标识接入 Hub 的应用。标志 `--app` 优先，否则取 `proxy.app`。
- **`proxy.insecure`（可选，默认 `false`）**：是否跳过 Hub 的 TLS 证书校验。该项以标志的「是否被显式设置」决定优先级：未显式给出 `--insecure` 时回退 `proxy.insecure`；显式设置（含 `--insecure=false`）时标志优先。映射为 `Insecure`。
- **`proxy.ca`（可选）**：自定义 CA bundle 路径（用于校验 Hub 证书）。标志 `--ca` 优先，否则取 `proxy.ca`。
- **`proxy.server_name`（可选）**：覆盖 TLS 握手时使用的 ServerName（SNI / 证书主机名）。标志 `--server-name` 优先，否则取 `proxy.server_name`。

#### 必填项校验与 mirror / forward 的差异

- **mirror（`bk proxy mirror`）必须配齐 `server`/`token`/`app`。** 三者经标志或 `proxy.*` 解析后任一为空，`resolveHubConfig` 即返回 **指明缺失项** 的错误，命令以 **非零退出码** 结束且不建立任何连接。错误信息仅列出缺失项名称（如 `server`、`token`、`app`），**绝不包含 token 明文**。
- **forward（`bk proxy forward`）对 hub 配置为「可选」，与 mirror 不同：** 当 `proxy.*` 不完整（解析出错）**或** 指定 `--direct` 时，forward 会 **回退到直连 `DirectDialer`（不报错、不建隧道）**；仅在 `proxy.*` 配齐且 **未** 指定 `--direct` 时，才经 yamux 隧道连接 Hub。即 forward 缺少 hub 配置不会失败，而是降级直连（来源：`cmd/proxyForward.go` 的 `selectForwardDialer`）。

#### forward 隧道前置条件与 Hub 侧安全约束

forward 走 yamux 隧道时，其安全模型的 **执行位于 Hub 侧**，bk 侧不绕过（对应 Requirement 5.5）：

- **前置条件：对应 app 须在 Hub 开启 forwarding。** 若该 app 未开启（`MaxForwarders` 默认 `0` 即 **禁用**，或未配置 `ForwardTargets`），隧道拨远端会被 **Hub 拒绝**；bk 会把该拒绝原因如实呈现给用户，并说明「该限制由 Hub 侧安全策略 `ForwardTargets`/`MaxForwarders` 执行，bk 不绕过」。
- **`ForwardTargets` allowlist 防 SSRF：** 远端目标必须落在 Hub 配置的 `ForwardTargets` allowlist 内，否则被拒绝。该 allowlist 与 `MaxForwarders` 上限均由 **Hub 侧** 配置与执行，bk 端无对应配置键、无法在客户端放宽。

#### `proxy.insecure=true` / `--insecure` 的安全风险

- **`proxy.insecure=true`（或 `--insecure`，默认 `false`）会跳过 Hub 的 TLS 证书校验。** 这会关闭对 Hub 服务器身份的验证，使连接易受 **中间人攻击（MITM）**，**仅供开发用途**（对应 Requirement 6.4 / 7.4）。
- **生产环境不应使用 `--insecure`；** 如需自定义信任根或修正主机名，应改为配置 `proxy.ca`（CA bundle）与 `proxy.server_name`（TLS ServerName），保留证书校验。

来源：`cmd/proxy.go`（标志定义、`resolveHubConfig`/`resolveHubConfigFrom` 的 viper key 读取、优先级与必填校验）、`cmd/proxyForward.go`（forward 的隧道/直连选择与 Hub 拒绝呈现）。

## 完整示例

```yaml
# .bs.yaml

# Supabase / API 端点（默认 https://supabase.blksails.cn，可用 --api-endpoint 覆盖）
api_endpoint: "https://supabase.blksails.cn"

# API 密钥（Supabase anon/api key）。敏感凭据，切勿提交到版本库。
api_key: "<your-supabase-anon-or-api-key>"

# SSH 连接块（顶层、全局）：供 dokku-management 通过 SSHConfig 消费。
ssh:
  host: "dokku.example.com"     # 必填：SSH 主机地址（缺失/空 → 报错）
  user: "dokku"                 # 可选：留空则由 dokku.New 默认为 dokku（不硬编码 root）
  port: 22                      # 可选：默认 22
  identity: "~/.ssh/id_ed25519" # 可选：私钥文件路径
  insecure: false               # 可选：默认 false；true 跳过 known_hosts 校验（有 MITM 风险，仅限受信/开发环境）

# proxy 连接块（顶层、全局）：供 port-proxy（mirror/forward）消费，独立于 ssh 块。
proxy:
  server: "hub.example.com:8443"  # 必填（mirror）：Hub 的 TLS 地址 host:port
  token: "<your-hub-token>"       # 必填（mirror）：共享认证 token（敏感凭据，勿提交）
  app: "demo"                     # 必填（mirror）：app_id
  insecure: false                 # 可选：默认 false；true 跳过 Hub TLS 证书校验（仅开发，有 MITM 风险）
  ca: "~/.config/bk/hub-ca.pem"   # 可选：自定义 CA bundle 路径
  server_name: "hub.internal"     # 可选：TLS ServerName（SNI / 证书主机名）覆盖
```

`proxy` 块装配后的常用命令示例：

```bash
# mirror：必须配齐 proxy.server/token/app（或经 --server/--token/--app 提供），否则报错非零退出
bk proxy mirror --target http://127.0.0.1:3000

# forward（隧道）：proxy.* 配齐且未 --direct → 经 yamux 隧道连接 Hub
bk proxy forward 8080:app.internal:80 9090:80

# forward（直连）：--direct 强制直连，即便 proxy.* 配齐也不建隧道（同网段联调/测试）
bk proxy forward --direct 5432:db.local:5432
```

## 消费方说明

- **`ssh` 块由 dokku-management 消费**：通过稳定共享入口 `SSHConfig(profile) (sshx.Config, error)`（`cmd/ssh_config.go`）读取并映射。`profile` 入参随签名冻结而保留，当前实现读取的是全局 `ssh` 块。
- **port-proxy 使用独立的 `proxy` 块、不使用 `ssh` 块**：端口代理能力（`bk proxy mirror`/`forward`）的远端可达性由 yamux+TLS Hub 隧道决定，读取其自有的 `proxy.*` 配置键（见上文 [`proxy` 块](#proxy-块)），**不** 依赖 `ssh.*` 块（两者相互独立，对应 Requirement 9.6 / Boundary）。
