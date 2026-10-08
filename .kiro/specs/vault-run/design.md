# 设计文档

## 概述

新增 `bk vault run <app> -- <command> [args...]`。命令复用 Vault 的身份、存储、主密钥和解密逻辑，在内存中构造子进程环境后直接执行，不经过 shell，也不产生明文 env 文件。

### 目标

- 原子解密并注入全部 secrets。
- 透传交互终端、工作目录、信号与退出码。
- 用可注入 runner 完成不触达真实进程和 Supabase 的单元测试。

### 非目标

- 不新增 Vault RBAC、审计或密钥轮换。
- 不改变存储 schema 和密文格式。

## 边界承诺

### 本 Spec 负责

- `vault run` 命令语法、环境合并、安全失败和执行语义。
- 根命令对实现 `ExitCode() int` 的错误保留退出码。

### 范围外

- `vault set/get/export` 行为和远端数据模型。

### 允许依赖

- `vaultListerFull`、`vault.Decrypt`、`vaultMasterKey`、`signalContext`、Go `os/exec`。

## 文件结构计划

- `cmd/vaultRun.go`：命令、原子解密、环境合并和进程 runner。
- `cmd/vaultRun_test.go`：成功、空集、失败不启动、覆盖、I/O 和命令装配测试。
- `cmd/root.go`：保留子进程退出码。
- `cmd/root_test.go`：退出码选择测试。
- `cmd/vault.go`、`README.md`、`skills.md`：帮助和用法更新。

## 组件与接口

### `runVaultRun`

输入 app、命令参数、主密钥、lister、decrypt、父环境和 runner。先 List，再全部 Decrypt，校验 key 并合并环境；全部成功后只调用 runner 一次。任何前置错误均保证 runner 调用次数为零。

### `processRunner`

接收 context、命令、参数和 `ProcessOptions`（Env、Dir、Stdin、Stdout、Stderr）。生产实现使用 `exec.CommandContext`；测试使用 fake。

### 退出码

生产 runner 直接返回 `*exec.ExitError`。`Execute` 使用 `errors.As` 查找 `ExitCode() int` 并以该状态退出，其余错误仍为 1。

## 测试策略

- 单元测试验证 Vault 覆盖父环境、参数和 I/O 透传。
- 解密中途失败、非法 key、List 失败均不得调用 runner或输出 secret。
- 空 Vault 仍运行。
- fake exit-code error 验证根命令选择相同退出码。
- 全量 `go test ./...` 与构建、`bk vault run --help` smoke。
