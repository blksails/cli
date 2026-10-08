# 实施任务

- [x] 1.1 为 OSS release 清单和 GitHub 回退编写失败测试
  - _Requirements: 2.1, 2.2, 2.3, 2.5_
  - _Boundary: cmd/update_

- [x] 1.2 实现 OSS 优先的 release 获取与安全下载
  - _Depends: 1.1_
  - _Requirements: 2.1, 2.2, 2.3, 2.4, 2.5_
  - _Boundary: cmd/update_

- [x] 2.1 增加 OSS 发布脚本和 GitHub Actions 上传步骤
  - _Depends: 1.2_
  - _Requirements: 1.1, 1.2, 1.3, 1.4_
  - _Boundary: release automation_

- [x] 2.2 更新下载文档并验证发布链路
  - _Depends: 2.1_
  - _Requirements: 3.1, 3.2_
  - _Boundary: docs and validation_

- [ ] 3.1 发布 v0.1.6 并验证 GitHub 与 OSS 公开产物
  - _Depends: 2.2_
  - _Requirements: 3.3_
  - _Boundary: external release_

## Implementation Notes

- OSS bucket 位于 `cn-hangzhou`，公开 HTTPS 基址为 `https://blksails-pi-desktop.oss-cn-hangzhou.aliyuncs.com`。
