# PR 流程规范

本文档说明 Narra 的 Pull Request（PR）提交流程，供功能开发、修复和文档更新使用。当前仓库以 `main` 为集成分支，已有 `feature/*` 分支通过 PR 合入 `main`；这里不要求另建 `develop` 分支。

## 1. 创建分支

从最新的 `main` 创建工作分支。命名应能说明改动内容；现有成员分支如 `feature/li` 可以继续使用，但在同一分支上连续开发多个需求时，建议每项需求单独发起 PR。

| 类型 | 分支示例 | 用途 |
| --- | --- | --- |
| 功能 | `feature/classroom-page-retry` | 新增功能 |
| 修复 | `fix/auth-token-refresh` | 修复缺陷 |
| 文档 | `docs/pr-process` | 文档调整 |
| 重构 | `refactor/mcp-registry` | 不改变外部行为的代码整理 |

```bash
git fetch origin
git switch main
git pull --ff-only origin main
git switch -c feature/classroom-page-retry
```

如果当前工作区有未提交改动，先确认其归属，再切换分支；不要为了执行上述命令丢弃现有改动。

## 2. 开发与提交

- 一个 PR 围绕一个可说明、可验证的目标。相关的后端、前端、迁移、测试和文档可以放在同一个 PR 中。
- 提交信息按 [Git 提交规范](Git提交规范.md)书写。不要把密钥、`.env`、`configs/config.yaml`、日志或本地工作文件加入提交。
- 修改接口、配置或数据库结构时，在 PR 描述里写明调用方影响、配置方法和迁移步骤。
- 修改课堂生成、异步任务或恢复流程时，说明失败与重试的处理方式；修改页面交互时附上关键状态的截图或录屏。

推送前查看提交范围及暂存内容：

```bash
git status --short
git diff --check
git diff --cached
git push -u origin feature/classroom-page-retry
```

## 3. 提交 PR

在 GitHub 上从工作分支向 `main` 发起 PR。标题概括业务变化，建议沿用提交信息格式，例如 `feat(classroom): 支持失败页面单独重试`。未完成但需要提前讨论时，使用 GitHub 的 Draft PR。

描述可以使用以下结构；没有关联 Issue 时删掉对应项，不要保留空的 `Closes #`：

```markdown
## 改动内容
- 解决了什么问题，用户或调用方会看到什么变化
- 关键实现与设计取舍

## 影响范围
- 接口、配置、数据库迁移或兼容性影响；没有则写“无”

## 验证
- 运行的测试或手工验证步骤及结果
- 未验证的部分及原因

## 关联 Issue
- Closes #编号（如有）

## 截图或录屏
- 涉及界面变化时提供（如有）
```

## 4. 检查与审查

提交 PR 前，根据改动范围完成检查。后端运行相关包测试，改动跨多个模块时运行 `go test ./...`；必要时运行 `go vet ./...`。前端在 `frontend/` 下运行 `npm run test` 和 `npm run build`。纯文档改动核对链接与排版。把实际结果写入 PR，不要把未运行的检查标为通过。

请相关模块成员审查 PR。审查重点包括：业务行为是否符合预期、异常及权限边界、并发与幂等、数据库迁移影响、测试覆盖，以及文档是否跟上改动。收到反馈后在同一工作分支修正，并回复关键问题的处理结果。

这里不假定仓库已配置自动检查或强制审批人数；若 GitHub 分支保护规则有要求，以实际规则为准。

## 5. 更新分支与合并

PR 与 `main` 冲突时，在工作分支上同步最新代码并解决冲突：

```bash
git fetch origin
git switch feature/classroom-page-retry
git merge origin/main
git status
```

逐个检查冲突文件，完成合并后运行受影响的测试，再推送分支。不要直接在 `main` 上处理 PR 冲突。已有 PR 使用过 GitHub 的合并提交，合并方式按仓库当前设置和审查结论选择，不强制重写已有提交历史。

合并前确认：PR 的改动范围清楚、审查意见已处理、验证结果可复现，且目标分支仍是 `main`。合并后按需删除已合并的工作分支。

## 相关文档

- [Git 提交规范](Git提交规范.md)
- [项目 README](../README.md)
