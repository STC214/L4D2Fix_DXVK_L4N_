# DXVK 更新功能增量审查

日期：2026-10-26（Asia/Shanghai）。沿用已完成的三个工坊平台更新与部署流程，不修改用户已有 README 内容。

## 功能

- 现有“检查最新版并下载”按钮同时检查 DXVK；来源为 [官方发布页](https://github.com/doitsujin/dxvk/releases?page=1)，通过 [GitHub latest release API](https://api.github.com/repos/doitsujin/dxvk/releases/latest) 获取稳定版元数据。
- 校验仓库、发布标签、唯一 Windows 二进制附件、附件大小及下载路径，排除 draft、prerelease、native 和源码包；支持 GitHub 官方附件重定向，拒绝非 HTTPS 或外来域。
- 保留 `.tar.gz` 格式；下载数据使用 `.part` 临时后缀，完成校验前不出现在版本列表。核对官方 SHA256（提供时）、gzip 完整性、路径/大小限制及唯一 x86 DLL 组合。
- 写入 `resources/dxvk-index.json` 并自动刷新、选中对应 DXVK 资源；刷新清空旧选项避免重复。缓存校验通过不重复下载，损坏时重下，历史版本保留。
- Steam 与 GitHub 独立检查，一个来源失败不跳过另一个；成功项保留，错误如限流记入日志。
- GitHub 短时连接异常及 5xx 最多额外重试两次，保留原始官方 URL；限流不循环重试，取消请求立即停止。真实联网复测曾出现连接超时，失败记录保留在证据目录，未发布残缺资源。
- 更新检查只准备资源，不修改游戏目录；仍由原 L4N＋DXVK 一键处理流程完成部署。

## 验证与回退

完整证据目录：`.gotmp/dxvk-update-review/`。

- `verification.json`：原始代码基线、新代码三轮回归、两轮 race、真实 GitHub 下载与缓存复用、配套工具、go vet、构建、便携版逐文件校验的精确命令和字面输出/退出码。
- `changes.patch`：本轮增量补丁。
- `rollback.ps1`、`rollback-manifest.json`、`original/`：本轮原文件与哈希；回退脚本默认仅校验，`-Apply` 显式执行恢复。
- `rollback-verification.json`：在隔离副本实际回退，验证原始文件哈希、移除新增文件及保留无关 README 的记录。

测试覆盖缓存复用、损坏重下、历史版本保留、错误仓库/附件、重复附件、预发布、下载截断、官方哈希不符、x64-only、路径穿越、gzip CRC 错误、API 限流、取消检查和两个更新来源独立执行。

未在实际游戏进程内测试新 DXVK 的显卡兼容性或帧率；文件校验和下载成功不等于游戏效果已验证。用户仍可选择原有 DXVK 版本。
