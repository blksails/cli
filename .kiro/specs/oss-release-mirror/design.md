# 设计文档

## 概览

GoReleaser 继续负责跨平台构建与 GitHub Release。GitHub Actions 在发布成功后安装固定版本的 ossutil，再调用 `scripts/publish-oss.sh` 将同一批产物同步到 OSS。

## 镜像布局

```text
https://blksails-pi-desktop.oss-cn-hangzhou.aliyuncs.com/
└── bk/releases/
    ├── latest.json
    └── v0.1.6/
        ├── release.json
        ├── checksums.txt
        ├── bk_0.1.6_darwin_arm64.tar.gz
        └── ...
```

清单沿用客户端现有 release 数据模型：

```json
{
  "tag_name": "v0.1.6",
  "name": "bk v0.1.6",
  "assets": [
    {"name": "checksums.txt", "url": "https://.../checksums.txt"}
  ]
}
```

## 客户端流程

1. 规范化 `--mirror` 基址。
2. 未禁用镜像时，请求 `releases/latest.json` 或 `releases/<tag>/release.json`。
3. 镜像失败时解析 GitHub Token 并请求 GitHub Release API。
4. 从清单选择当前平台归档和校验文件。
5. 下载 URL 仅在 GitHub API 主机上附加 Authorization；OSS 下载保持匿名。
6. 校验并原子替换可执行文件。

## 边界约束

- `cmd/update.go` 负责编排和 HTTP 来源选择。
- `internal/selfupdate` 继续只负责平台匹配、校验、解包和替换，不引入网络依赖。
- `scripts/publish-oss.sh` 只处理已有 `dist` 产物，不负责构建和打 tag。
- GitHub Release 仍是可靠回退和历史发布记录。

## 验证

- 单元测试覆盖 OSS 成功、指定版本、OSS 失败回退、无 token 错误和跨主机凭据隔离。
- `go test ./...`、`go vet ./...`、GoReleaser 配置检查、构建与 `bk update --help`。
- 发布后对 `latest.json`、归档和 checksum 执行公开 HTTPS 请求。
