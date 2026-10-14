# Git 提交规范

本文档约定 Narra 后续的提交方式。历史提交使用过中文自然语言描述，无需为套用本规范而改写历史。

## 提交信息

采用 Conventional Commits 格式，范围可省略：

```text
<type>(<scope>): <subject>

[可选：说明改动原因、实现要点或兼容性影响]
```

标题使用中文，写清具体改动，不加句号；建议控制在 50 字以内。一个提交只表达一项完整改动。复杂改动可在空行后写正文，说明为什么修改及需要注意的行为变化。

```text
feat(classroom): 支持失败页面单独重试
fix(auth): 修复刷新令牌后用户状态未更新
docs: 补充本地启动步骤
```

## 类型与范围

| 类型 | 用途 |
| --- | --- |
| `feat` | 新增用户可见的功能或能力 |
| `fix` | 修复已有功能的错误 |
| `docs` | 仅修改文档 |
| `refactor` | 调整实现，不改变对外行为 |
| `perf` | 优化性能或资源使用 |
| `test` | 增加或修改测试 |
| `build` | 修改依赖或构建配置 |
| `ci` | 修改持续集成配置（如果后续引入） |
| `chore` | 其他维护工作 |
| `revert` | 回退已有提交 |

`scope` 写主要受影响的模块，可以省略。常用范围如下；新增模块可直接使用对应的清晰名称。

| 范围 | 对应内容 |
| --- | --- |
| `classroom` | 课程规划、页面生成、审核与恢复（`internal/agent/classroom/`） |
| `discussion` | 课堂讨论 Agent（`internal/agent/discussion/`） |
| `worker` | Asynq 任务与消费恢复（`internal/worker/`、`internal/bootstrap/`） |
| `rag` | 知识库收录与检索（`internal/rag/`） |
| `mcp` | MCP 连接、工具注册与调用（`internal/mcp/`） |
| `auth` | 登录、鉴权与令牌处理 |
| `api` | 通用 API 接口与路由 |
| `frontend` | Vue 前端通用改动 |
| `db` | 数据模型、Repository 与数据库迁移 |

跨多个模块的改动，优先按可独立验证的功能拆分提交；确实不可拆分时，使用最能说明业务目标的范围，或省略范围。

## 提交粒度

- 每次提交保持单一目的：功能、修复、测试和文档可以放在同一提交中，只要它们共同完成同一项改动。
- 把不相关的格式化、配置调整或其他模块修改分开提交。不要为了按代码层次拆分，把无法单独运行或验证的半成品提交出去。
- 提交测试代码、必要的配置样例和已确认要公开的文档。不要提交 `.env`、`configs/config.yaml`、密钥、日志、构建产物和本地工作记录。
- 合并冲突解决后检查最终文件内容，再完成合并提交；Git 自动生成的合并提交信息可以保留。

## 提交前检查

先检查改动范围和暂存内容：

```bash
git status --short
git diff --check
git diff --cached
```

按改动选择验证命令。后端改动运行相关包测试，涉及公共行为时运行 `go test ./...`；前端改动在 `frontend/` 下运行 `npm run test` 和 `npm run build`；必要时运行 `go vet ./...`。纯文档改动检查链接和排版即可。

使用路径或 `git add -p` 暂存本次改动，再核对暂存区。不要直接把工作区里无关的文件一起提交：

```bash
git add <本次改动的路径>
git diff --cached --check
git diff --cached --stat
git commit -m "feat(classroom): 支持失败页面单独重试"
```

如果提交引入不兼容的接口或数据变更，在正文中说明影响与迁移办法；需要明确标注破坏性变更时，使用 `BREAKING CHANGE:` 页脚。
