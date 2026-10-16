# Narra 两台 2 核 2G 服务器部署

同一个 GitHub 仓库保存两台服务器的配置。应用机运行宿主机 Nginx、Docker API；数据机运行 Docker PostgreSQL + pgvector、Redis。GitHub Actions 构建镜像和前端，并且**只 SSH 到应用机**更新代码。数据机不需要 GitHub Secrets，也不需要部署整个仓库；首次放入 `compose.data.yml` 并启动即可。

## 1. 先准备数据机

两台机器要能经阿里云内网互通，最好同地域、同 VPC。数据机需安装 Docker Compose，不需要 Nginx。数据机所在网络无法直接拉取 Docker Hub 的 `pgvector/pgvector:pg17`，阿里云镜像加速器也返回 `not found`。仓库提供手动触发的 `Publish data images` 工作流：它在 GitHub 托管的构建机上拉取 pgvector 和 Redis，再转存到本仓库 GHCR。首次使用前，把 `.github/workflows/publish-data-images.yml` 合入 `main`，到 GitHub Actions 手动运行该工作流。

工作流成功后，在 GitHub 仓库的 Packages 页面把 `narra-postgres` 和 `narra-redis` 两个包设为 **Public**，数据机便能无凭据拉取。若仓库或组织策略不允许公开镜像，需要另行配置具有 `read:packages` 权限的 GHCR 凭据到数据机；不要把长期令牌写入仓库或 Compose。

然后在数据机运行 `mkdir -p /opt/narra/deploy`，把仓库中的 `deploy/compose.data.yml` 上传为 `/opt/narra/deploy/compose.data.yml`，按 `deploy/env.data.example` 建 `/opt/narra/.env`：

- `DATA_BIND_IP` 填数据机**内网 IP**，不能填公网 IP 或 `0.0.0.0`。
- `POSTGRES_PASSWORD`、`REDIS_PASSWORD` 用各自的强随机值，例如 `openssl rand -hex 24`。
- 不把真实 `.env`、密码或 JWT 密钥提交到 GitHub。

在数据机运行：

```bash
chmod 600 /opt/narra/.env
docker compose --env-file /opt/narra/.env -f /opt/narra/deploy/compose.data.yml up -d --wait
docker compose --env-file /opt/narra/.env -f /opt/narra/deploy/compose.data.yml ps
```

阿里云安全组与服务器防火墙都要把数据机的 **5432、6379 仅允许应用机内网 IP** 访问；不对公网开放。PostgreSQL、Redis 的数据分别在 Docker 命名卷 `narra-data_pgdata` 和 `narra-data_redisdata`。不要运行 `docker compose down -v`。

如果之前已把旧版 `compose.data.yml` 传到数据机，需重新上传更新后的文件。数据机可先执行 `docker pull ghcr.io/qianqiang-del/narra-postgres:pg17` 验证镜像可用，再运行上述 Compose 命令。

## 2. 再准备应用机

应用机安装 Docker Compose 和 Nginx，CPU 架构 `x86_64`。当前已装 Nginx 的服务器可以继续使用。创建目录：

```bash
mkdir -p /opt/narra/{deploy,data,web/releases,web/incoming}
chmod 755 /opt/narra /opt/narra/web /opt/narra/web/releases
```

按 `deploy/env.app.example` 建 `/opt/narra/.env`，`POSTGRES_HOST` 与 `REDIS_HOST` 都填数据机内网 IP；密码与数据机 `.env` 完全一致。`JWT_SECRET` 用 `openssl rand -hex 48` 生成，上线后保持不变；它还用于解密用户保存的模型密钥。执行 `chmod 600 /opt/narra/.env`。API 仅绑定应用机的 `127.0.0.1:8080`，由 Nginx 反代。

在应用机检查到数据机的端口：

```bash
timeout 3 bash -c '</dev/tcp/数据机内网IP/5432' && echo 'PostgreSQL 可达'
timeout 3 bash -c '</dev/tcp/数据机内网IP/6379' && echo 'Redis 可达'
```

这里仅验证 TCP 连通；密码是否正确要在 API 启动时确认。应用机的 `/opt/narra/data` 保存上传文件和本地音频，也需备份。

## 3. GitHub Actions 发布

现有工作流 `.github/workflows/deploy.yml` 监听 `main`，使用这四个仓库 Secrets：`DEPLOY_SSH_HOST`、`DEPLOY_SSH_USER`、`DEPLOY_SSH_KEY`、`DEPLOY_SSH_KNOWN_HOSTS`。它们都是**应用机**的 SSH 信息；数据机不用填第二套。后端镜像推送 GHCR 使用 Actions 自带的 `GITHUB_TOKEN`。

确认数据机健康、应用机 `.env` 已填写后，将 `Dockerfile`、`.dockerignore`、`configs/config.production.yaml`、`deploy/`、`.github/workflows/deploy.yml` 提交并合入 `main`。工作流构建后端和前端，SSH 到应用机拉取镜像、启动 API，健康检查通过才切换前端。以后合入 `main` 会重复这一步。数据机的 Compose 配置若有改动，需要单独传到数据机执行 `docker compose up -d`；日常代码更新不需要动它。

首次发布后，把 `/opt/narra/deploy/nginx.conf` 安装到应用机的 `/etc/nginx/conf.d/narra.conf`，运行 `nginx -t && systemctl reload nginx`。若默认站点占用 80 端口，先禁用默认站点。修改 Nginx 配置后也需重新加载。正式公开服务前设置域名和 HTTPS。CentOS Stream 9 使用 SELinux 时，如 Nginx 访问 `/opt/narra/web/current` 返回 403，或反代 8080 返回 502，先检查 Nginx 错误日志和 SELinux 审计日志，再分别设置静态目录的 `httpd_sys_content_t` 类型及 `httpd_can_network_connect`；不要直接关闭 SELinux。

## 上线核对

- 两台均为 2 核 2G 时，保持 `configs/config.production.yaml` 的并发为 1，关闭本地文档解析。观察 `docker stats`、`free -h`；若 OOM 或 swap 长期增长，升级内存。应用镜像和前端由 Actions 构建，不在服务器构建。
- 新库的表和 pgvector 扩展由 API 启动时自动创建。执行一次 `migrations/0002_seed_preset_agents.sql` 为已有账号补角色，并让后续注册账号自动获得角色，见 `migrations/README.md`「第二步」。存量库则先备份并按 `migrations/0012_private_ownership.md` 迁移。
- `config.production.yaml` 默认关闭短信注册和平台模型，正式使用前要准备可登录账号和可用模型。数据机需备份 PostgreSQL、Redis 卷和 `.env`；应用机需备份 `/opt/narra/data` 与 `.env`，备份应保存在其他机器或对象存储。
- 此前用于 GitHub Actions 的 SSH 私钥曾被贴到聊天，正式上线前应更换部署密钥并更新 `DEPLOY_SSH_KEY`，移除服务器上的旧公钥。
