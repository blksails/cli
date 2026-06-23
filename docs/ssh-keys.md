# SSH 密钥发放（bk ssh-key）

本文档说明如何用 `bk ssh-key` 为客户/团队成员发放 Dokku 主机的 SSH 访问密钥。

## 设计原则

> **私钥永不离开生成它的机器。**

整套流程围绕这一原则设计，分两个角色协作：

- **客户/成员**：在自己机器上生成密钥对，私钥本地留存（`0600`），只把**公钥**登记到后端（状态 `pending`）。
- **管理员**：借自己既有的 Dokku 主机 SSH 接入，把审核通过的 pending 公钥**代装**到 Dokku（状态 `installed`），或**吊销**。

管理员全程不接触客户私钥，这是安全边界。

## 状态机

```
provision            install (管理员)         revoke (管理员)
   │                      │                        │
   ▼                      ▼                        ▼
 pending ───────────► installed ───────────────► revoked
（已生成并登记公钥）  （已写入 Dokku 主机）      （已从 Dokku 移除）
```

## 命令一览

| 命令 | 角色 | 作用 |
| --- | --- | --- |
| `bk ssh-key provision` | 客户/成员 | 本机生成 ed25519 密钥对，登记公钥为 `pending` |
| `bk ssh-key list` | 客户/成员 | 查看自己登记的密钥及状态 |
| `bk ssh-key install [<名称\|指纹>...]` | 管理员 | 把指定（或 `--all`）pending 公钥代装到 Dokku |
| `bk ssh-key revoke <名称\|指纹>` | 管理员 | 吊销密钥并从 Dokku 移除 |

---

## 标准流程

### 1. 客户侧：生成并登记密钥

在**客户自己的机器**上：

```bash
# 先登录（密钥归属邮箱、名称从登录会话派生）
bk auth login -u <客户邮箱>

# 生成密钥对并登记公钥
bk ssh-key provision
```

`provision` 行为：

- 本机生成一对 **ed25519** 密钥，私钥以 `0600` 落盘且**永不上传**。
- 只把**公钥 + 指纹 + 名称 + 目标主机**登记到 Supabase，初始状态 `pending`。
- 密钥名按 `邮箱 + host` 自动派生。

可选 flag：

| flag | 说明 |
| --- | --- |
| `--host <主机>` | 目标主机；默认取 `.bs.yaml` 的 `ssh.host` |
| `--set-identity` | 自动把 `.bs.yaml` 的 `ssh.identity` 指向新生成的私钥 |
| `--email <邮箱>` | 显式指定归属邮箱（无登录会话时使用） |
| `--force` | 覆盖已存在的私钥（默认拒绝覆盖正在使用的私钥） |

### 2. 管理员侧：审核并代装

在**管理员机器**上（需已配置 Dokku 主机 SSH 接入，见 [config.md](config.md) 的 `ssh:` 块）：

```bash
# 先列出全部 pending 供审核（不带参数时仅列出，不代装任何一条）
bk ssh-key install

# 审核后代装指定的那条（按名称或指纹）
bk ssh-key install <名称|指纹>           # dokku 用户模型
bk ssh-key install <名称|指纹> --sudo    # root / 普通管理员模型

# 或一次性代装全部 pending
bk ssh-key install --all
```

代装行为：

- 借管理员既有 SSH 接入，在 Dokku 主机执行 `ssh-keys` 添加（先移除同名再添加，**幂等**）。
- 成功后把记录回写为 `installed`，并记录安装者与安装时间。
- 输出**绝不包含任何私钥内容**。
- **安全闸门**：不带参数且不带 `--all` 时只列出、不代装——必须显式选择，避免任意 provision 的 pending 被无差别放行进 Dokku。
- 非管理员被 RLS 拒绝时提示需管理员权限并以非零退出码结束。

### 3. 查看与吊销

```bash
# 客户查看自己的密钥与状态（pending / installed / revoked）
bk ssh-key list

# 管理员吊销密钥并从 Dokku 移除
bk ssh-key revoke <名称|指纹>
bk ssh-key revoke <名称|指纹> --sudo
```

`revoke` 行为：

- 按指纹或名称定位记录，在 Dokku 主机执行 `ssh-keys` 移除，回写 `revoked` 并记录吊销者与时间。
- 目标不存在或已是 `revoked` 时友好提示并以零退出码结束（**幂等**）。
- Dokku 侧移除失败时显示清晰错误、以非零退出码结束，且**不会**把状态误标为 `revoked`。

---

## `--sudo` 怎么选

取决于管理员的 Dokku 接入模型（见 [config.md](config.md) 的 `ssh.user`）：

| `ssh.user` | 是否加 `--sudo` | 说明 |
| --- | --- | --- |
| `dokku` | **不加** | dokku 强制命令模型，dokku 用户自带受限命令集 |
| `root` / 普通 sudoer | **加 `--sudo`** | 实际执行 `sudo dokku ...` |

---

## 客户无法自行运行 bk 时（不推荐）

若客户无法安装 bk，管理员可代为生成——但这会**打破"私钥不离开本机"的原则**，需把私钥安全交付客户（避免明文走邮件/IM）：

```bash
# 在管理员机器上替客户生成
bk ssh-key provision --email <客户邮箱> --host <主机>
# 私钥位于本机 ~/.ssh/ 下，经加密渠道 / 一次性分发交给客户
bk ssh-key install <名称>
```

**更推荐**：让客户安装 bk 自行 `provision`，管理员只负责 `install`。

---

## 相关文档

- [config.md](config.md) — `.bs.yaml` 配置，含 `ssh:` 块（`ssh.host` / `ssh.user` / `ssh.port` / `ssh.identity` / `ssh.insecure`）
