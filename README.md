# bk —— BlackSails Cloud CLI

> 黑帆云命令行工具：管理 Dokku 应用、发布与配置应用、本地代理（HTTP 镜像 / TCP 转发）、SSH 密钥发放与加密 Secret Vault。

[![Go Version](https://img.shields.io/badge/go-1.25%2B-00ADD8?logo=go)](go.mod)
[![Release](https://img.shields.io/badge/release-goreleaser-326CE5?logo=goreleaser)](.goreleaser.yaml)

## 功能概览

`bk` 是黑帆云的统一命令行入口，主要能力：

| 命令族 | 说明 |
| --- | --- |
| `bk app` | 管理 Dokku 应用：列举 / 创建 / 销毁 / 配置 / 进程 / 日志 / 重启 / 扩缩容 / 部署 remote |
| `bk auth` | 用户认证管理（登录 / 登出 / 当前身份） |
| `bk proxy` | 本地代理命令族：HTTP 流量镜像（mirror）/ TCP 端口转发（forward） |
| `bk vault` | Secret Vault：本机加密存储、可经 Supabase 共享 |
| `bk ssh-key` | SSH 密钥发放：生成 / 登记 / 代装 / 吊销 |
| `bk access-key` | 访问密钥管理 |
| `bk doctor` | 环境自检 |
| `bk update` | 自升级到最新版本（别名 `upgrade`） |
| `bk version` | 显示版本与构建信息 |

## 安装

### 方式一：下载预编译二进制（推荐）

从 [Releases](../../releases) 页面下载对应平台的归档包，支持：

| 操作系统 | 架构 |
| --- | --- |
| Linux | amd64 / arm64 |
| macOS | amd64 (Intel) / arm64 (Apple Silicon) |
| Windows | amd64 / arm64 |

解压后将 `bk`（Windows 为 `bk.exe`）放入 `PATH` 即可：

```bash
# 以 Linux amd64 为例
tar -xzf bk_<version>_linux_amd64.tar.gz
sudo install -m 0755 bk /usr/local/bin/bk
bk version
```

下载后可用 `checksums.txt` 校验完整性：

```bash
sha256sum -c checksums.txt --ignore-missing
```

### 方式二：从源码安装

需要 Go 1.25+：

```bash
make install          # 构建并安装到 GOBIN（回退 $(go env GOPATH)/bin）
# 或
go install pkg.blksails.net/bk@latest
```

### 方式三：本地构建

```bash
make build            # 产物在 ./bin/bk
./bin/bk version
```

### 方式四：自升级（已装过 bk）

```bash
bk update             # 升级到最新版（交互确认）
bk update --check     # 仅检查是否有新版本
bk update -y          # 跳过确认直接升级
bk update --version v0.1.1   # 安装指定版本
```

`bk update`（别名 `bk upgrade`）从 GitHub Releases 拉取最新版、校验 sha256 后原子替换当前二进制。

> 仓库为私有，下载需 GitHub token：自动按 `--token` > `GH_TOKEN` > `GITHUB_TOKEN` 环境变量 >
> 已登录的 `gh`（`gh auth token`）解析。若 bk 装在系统目录（如 `/usr/local/bin`）而当前用户无写权限，
> 需用 `sudo` 运行或重新安装。

## 快速开始

```bash
# 查看版本
bk version

# 登录
bk auth login

# 查看当前身份
bk auth whoami

# 列举应用
bk app ls

# 创建应用
bk app create myapp

# 设置应用配置
bk app config:set myapp KEY=VALUE

# 查看应用日志
bk app logs myapp

# 环境自检
bk doctor
```

## 把项目接入部署

`bk` 走 Dokku「git push 即部署」模型：`bk` 负责把周边配好，`git push` 触发构建部署。

```bash
# 1) 在项目仓库里添加 Dokku 部署 remote（主机地址自动从在线主机目录解析）
bk app remote myapp                 # 添加 remote dokku → dokku@<主机>:myapp
bk app remote myapp --print         # 只看 URL，不改仓库

# 2) 配置运行时环境与密钥
bk app config:set myapp KEY=VALUE   # 运行时环境变量
bk vault set myapp SECRET=...        # 加密 secret

# 3) 部署（推到部署分支，通常 main）
git push dokku main
```

> `bk app remote` 的主机地址优先取 `.bs.yaml` 的 `ssh.host`，否则用登录后缓存的在线主机目录
> （`bk host ls`）；git 推送用户固定为 `dokku`。remote 默认名为 `dokku`（刻意不叫 `origin`，
> 避免覆盖你的 GitHub `origin`）。应用尚不存在时先 `bk app create myapp`。

## 配置

`bk` 通过配置文件 + 环境变量 + 命令行 flag 三层来源管理配置，优先级为：

```
命令行 flag  >  环境变量  >  配置文件
```

- 默认配置文件：在用户主目录与当前目录查找 `.bs.yaml`
- 通过 `--config <path>` 指定配置文件
- 通过 `--profile <name>` 切换配置档（默认 `default`）

常用全局 flag：

| Flag | 说明 | 默认值 |
| --- | --- | --- |
| `--config` | 配置文件路径 | 自动查找 `.bs.yaml` |
| `--api-endpoint` | API 端点 | `https://supabase.blksails.cn` |
| `--api-key` | API 密钥（Supabase anon key） | 内置生产 anon key（可覆盖） |
| `--profile` | 配置档名称 | `default` |

更多配置细节见 [`docs/config.md`](docs/config.md)。

SSH 密钥发放（为客户/成员生成并代装 Dokku 访问密钥）见 [`docs/ssh-keys.md`](docs/ssh-keys.md)。

proxy 转发目标 allowlist 的中心化管理见 [`docs/proxy-targets.md`](docs/proxy-targets.md)。

## 开发

```bash
make help             # 列出所有可用目标
make build            # 本地构建到 ./bin/bk
make test             # 跑单元/集成测试
make e2e              # 跑全生命周期 e2e（hermetic，无需外部依赖）
make e2e-real         # 跑真实主机 e2e（需环境变量，见 e2e/README.md）
make install          # 构建并安装到 GOBIN
make clean            # 清理构建产物
```

## 发布（GoReleaser）

本仓库使用 [GoReleaser](https://goreleaser.com) 进行多平台发布，配置见 [`.goreleaser.yaml`](.goreleaser.yaml)。

### 本地验证（快照，不发布、不打 tag）

```bash
make snapshot         # 等价于 goreleaser release --snapshot --clean
# 产物在 ./dist，包含全部平台的归档与二进制
```

### 正式发布

发布需要一个已配置的 GitHub remote 与 `GITHUB_TOKEN`：

```bash
# 1) 打 tag（语义化版本）
git tag v0.1.0
git push origin v0.1.0

# 2) 发布（GoReleaser 自动构建多平台、生成 changelog、上传 Release 资产）
export GITHUB_TOKEN=<your-token>
make release          # 等价于 goreleaser release --clean
```

校验配置：

```bash
make release-check    # goreleaser check（需已配置 git remote）
```

> 版本号、commit、构建时间会在构建时通过 `-ldflags -X` 注入到 `pkg.blksails.net/bk/cmd` 包，
> 运行 `bk version` 可查看。`make build` / `make install` 也会注入 best-effort 的版本信息（取自 `git describe`）。

### CI 自动发布（GitHub Actions 示例）

在仓库 `.github/workflows/release.yml` 中：

```yaml
name: release
on:
  push:
    tags: ["v*"]
permissions:
  contents: write
jobs:
  goreleaser:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: actions/setup-go@v5
        with: { go-version: "1.25" }
      - uses: goreleaser/goreleaser-action@v6
        with:
          version: "~> v2"
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

## License

见 [LICENSE](LICENSE)。
