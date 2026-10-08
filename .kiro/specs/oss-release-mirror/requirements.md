# 需求文档

## 项目描述

发布新的 bk 版本，并把 GoReleaser 产物同步到 `oss://blksails-pi-desktop`，为中国网络环境提供无需 GitHub Token 的公开下载与自升级镜像。

## 需求

### 1. OSS 发布镜像

1.1 发布流水线必须把归档文件和 `checksums.txt` 上传到 `bk/releases/<tag>/`。

1.2 流水线必须发布 `bk/releases/<tag>/release.json` 与 `bk/releases/latest.json`，清单包含版本号、资产名称和公开 HTTPS 地址。

1.3 版本化资产必须使用长期不可变缓存；`latest.json` 必须禁止缓存，避免客户端长期停留在旧版本。

1.4 OSS 上传凭据只能来自 GitHub Actions secrets，不能写入仓库或日志。

### 2. 客户端镜像下载

2.1 `bk update` 必须优先从默认 OSS 镜像读取最新版或指定版本清单。

2.2 OSS 可用时，客户端不应要求 GitHub Token，并且不得把 GitHub Token 发送给 OSS 或其他非 GitHub 主机。

2.3 OSS 获取失败时，客户端必须回退现有 GitHub Releases 流程；此时才要求 GitHub Token。

2.4 下载后必须继续使用 `checksums.txt` 校验归档，再执行原子替换。

2.5 用户必须可以通过 `--mirror` 覆盖镜像基址，也可以用 `--mirror off` 禁用镜像。

### 3. 发布与文档

3.1 README 必须给出 OSS 镜像地址和直接下载布局。

3.2 发布流程必须通过测试、静态检查、构建和真实公开地址冒烟验证。

3.3 本次版本必须发布为现有 `v0.1.5` 之后的补丁版本 `v0.1.6`。
