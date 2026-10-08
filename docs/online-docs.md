# 在线文档

`bk docs` 通过 provider 机制集成在线文档服务，并直接复用当前 `bk auth` profile 的 Supabase 会话。当前支持 `tdocs`（腾讯文档），无需再单独安装或登录 `tdocs` 客户端；该结构为后续接入飞书预留。

## 首次使用

```bash
bk auth login
bk docs auth --provider tdocs       # 输出腾讯文档 OAuth 授权链接
# 在浏览器打开链接并完成授权
bk docs status
```

授权按当前用户及公司隔离。切换 `--profile` 时，文档身份也随之切换。

## 常用命令

```bash
bk docs ls                              # 递归列出文件
bk docs ls --provider tdocs             # 显式选择腾讯文档
bk docs ls 项目资料                       # 列出文件夹
bk docs ls --type doc --query 发布         # 过滤

bk docs cat 发布计划                      # 按标题读取 Markdown
bk docs cat 项目资料/发布计划               # 按路径读取
bk docs cat <file-id> -f txt -o note.txt
bk docs cat 数据表 -f csv                  # 表格导出为 CSV 文本

bk docs append 发布计划 "新增一段"
echo "新增一段" | bk docs append <file-id>
bk docs append <file-id> -i note.md
# append 自动识别在线表格；CSV/TSV 会写入第一张子表的连续空行
printf 'A131,任务一,P0\nA132,任务二,P0\n' | bk docs append 任务表

bk docs sheet ls 任务表
bk docs sheet read 任务表 工作表1 A1:D20
printf 'A131,任务一,P0\n' | bk docs sheet write 任务表 工作表1 A21:C21
bk docs sheet clear 任务表 工作表1 A21:C21
bk docs sheet add 任务表 归档 --rows 200 --cols 10
bk docs sheet rm 任务表 归档
bk docs sheet delete-rows 任务表 工作表1 21 22
bk docs sheet delete-cols 任务表 工作表1 4 5

bk docs download 发布计划                  # 默认 docx
bk docs download 数据表 -f xlsx
bk docs download <file-id> -f pdf -o out.pdf

bk docs upload report.docx
bk docs upload data.xlsx --folder <folder-id>
bk docs mkdir 项目资料
bk docs mkdir 子目录 --parent <folder-id>
```

文档参数可使用文件 ID、唯一标题或 `目录/文件` 路径。标题不唯一时命令会要求改用完整路径或 ID，避免操作错误文件。

`sheet write` 会覆盖指定区域，适合精确修改；`append` 会在写入前再次确认目标区域为空，检测到已有数据时会取消操作。

## Provider 与默认值

命令行通过 `--provider` 选择文档来源：

```bash
bk docs ls --provider tdocs       # 腾讯文档
```

不传 `--provider` 时，读取默认 provider。优先级为：

```text
--provider > BK_DOCS_PROVIDER > .bs.yaml 的 docs.provider > tdocs
```

可以在 `.bs.yaml` 中选择默认 provider：

```yaml
docs:
  provider: tdocs
```

当前仅注册 `tdocs`；传入尚未实现的 `feishu` 会返回明确错误，不会误操作腾讯文档。

## 服务地址

默认连接生产服务：

```text
https://tdocs.apps.blksails.cn
```

本地开发或私有部署可用任一种方式覆盖：

```bash
bk docs ls --docs-endpoint http://localhost:9070
BK_DOCS_ENDPOINT=http://localhost:9070 bk docs ls
```

也可写入 `.bs.yaml`：

```yaml
docs:
  provider: tdocs
  endpoint: http://localhost:9070
```

兼容原 tdocs 客户端的 `TDOCS_SERVER_URL` 环境变量；`BK_DOCS_ENDPOINT` 优先。
