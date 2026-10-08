# 调研与设计决策

## 摘要

- **功能**：vault-run
- **类型**：现有 CLI 的简单扩展
- **关键发现**：现有 `Store.List` 已提供按 app 获取密文记录；`vault.Decrypt` 与 `vaultMasterKey` 可直接复用；`signalContext` 已定义 SIGINT/SIGTERM 上下文。

## 设计决策

### 决策：先全部解密，再启动子进程

- **选择**：在内存中完成全部解密与环境构造，任一失败时不调用 runner。
- **原因**：延续 `vault export` 的原子解密安全不变量，避免部分 secret 被注入或子进程在不完整配置下启动。

### 决策：使用参数数组而非 shell

- **选择**：通过 `exec.CommandContext(command, args...)` 执行。
- **原因**：避免 shell 展开和命令注入，并保持参数原样。

## 风险与缓解

- Secret 存在于 BK 与子进程内存/环境中：这是运行所必需；禁止输出并仅传给目标子进程。
- 环境 key 非法：在执行前集中验证，失败时不启动命令。
- 子进程退出码被 Cobra 折叠：根命令识别 `ExitCode() int` 错误并保留状态码。
