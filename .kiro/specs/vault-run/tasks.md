# 实施计划

- [x] 1.1 为 `vault run` 编写失败和成功路径单元测试
  - 覆盖原子解密、环境覆盖、空 Vault、非法 key、参数及 I/O 透传。
  - _Requirements: 1.1, 1.2, 1.3, 1.4, 2.1, 2.2, 2.3_
  - _Boundary: cmd/vaultRun_test.go_

- [x] 1.2 实现 `vault run` 命令和进程执行适配器
  - 全部解密成功后才构造并启动 `exec.CommandContext`。
  - 注册命令并更新父命令帮助。
  - _Requirements: 1.1, 1.2, 1.3, 1.4, 2.1, 2.2, 2.3, 2.5_
  - _Depends: 1.1_

- [x] 1.3 保留子进程退出码并补根命令测试
  - 非零子进程退出码由 BK 原样返回，其余错误仍返回 1。
  - _Requirements: 2.4_
  - _Depends: 1.2_

- [x] 1.4 更新文档并完成全量验证
  - README、skills 和 `vault --help` 展示新命令。
  - `go test ./...`、构建及 smoke 全部通过。
  - _Requirements: 1.1, 2.1_
  - _Depends: 1.2, 1.3_
