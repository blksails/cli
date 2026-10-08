# 需求文档

## 简介

为 Secret Vault 增加本地进程注入命令，使用户无需把明文 secret 输出到终端或写入 `.env`，即可用指定 app 的全部 secret 启动子进程。

## 边界

- **范围内**：读取当前身份下指定 app 的 secrets、全部解密、注入子进程环境、透传终端与退出状态。
- **范围外**：Vault 授权/RBAC、secret 创建方式、远程 Dokku 注入、修改现有加密或存储格式。

## 需求

### Requirement 1: 安全注入并执行子进程

**Objective:** 作为 CLI 用户，我希望用 Vault secrets 启动本地命令，从而避免明文文件和终端输出。

#### Acceptance Criteria
1. When 用户执行 `bk vault run <app> -- <command> [args...]`，the BK CLI shall 解密该 app 的全部 secrets，并仅在全部成功后启动子进程。
2. When 子进程启动，the BK CLI shall 保留父进程环境，并由 Vault 中的同名 key 覆盖对应环境变量。
3. The BK CLI shall 将 stdin、stdout、stderr 与当前工作目录透传给子进程，且不得主动输出 secret 明文。
4. When app 不含 secrets，the BK CLI shall 仍以原环境启动命令。

### Requirement 2: 失败与退出语义

**Objective:** 作为脚本调用者，我希望可靠判断命令是否启动及其退出结果。

#### Acceptance Criteria
1. If 参数缺少 app 或 command，then the BK CLI shall 在启动子进程前返回用法错误。
2. If secret 列举或任一解密失败，then the BK CLI shall 不启动子进程，也不得泄露已解密明文。
3. If secret key 不能表示为合法进程环境变量名，then the BK CLI shall 在启动前返回错误。
4. When 子进程以非零状态退出，the BK CLI shall 返回相同退出码。
5. When 收到 SIGINT 或 SIGTERM，the BK CLI shall 取消子进程执行并完成退出。
