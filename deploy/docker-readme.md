# 前后端一起 Docker 部署（CLIProxyAPI + Web UI）

本 README 说明如何用 Docker 同时部署后端（CLIProxyAPI）与前端管理面板
（Cli-Proxy-API-Management-Center），二者同源访问、无需配置跨域。

部署文件分布：

| 文件 | 位置 | 说明 |
| --- | --- | --- |
| `docker-compose.full.yml` | 后端仓库根目录 | 全栈编排（web + backend） |
| `Dockerfile` | 后端仓库根目录 | 后端镜像（已有，含 `/healthz` 健康检查） |
| `Dockerfile` | 前端仓库根目录 | 前端镜像（bun 构建单文件 → nginx） |
| `nginx/default.conf.template` | 前端仓库根目录 | nginx 模板：托管 SPA + 反代 `/v0/` |

## 目录结构要求

两个仓库需为**同级目录**（compose 用相对路径引用）：

```
<parent>/
├── CLIProxyAPI/                      # 后端（本仓库）
│   └── docker-compose.full.yml       # ← 在此目录执行 compose
└── Cli-Proxy-API-Management-Center/  # 前端
```

## 快速开始（本地构建）

```bash
cd CLIProxyAPI

# 准备 config.yaml（后端配置，含管理密钥）
cp config.example.yaml config.yaml
#   编辑 config.yaml：
#   - port: 8317                # 默认即可
#   - remote-management:
#       allow-remote: true      # 必须：nginx 反代访问，非 localhost
#       secret-key: <你的管理密钥>

docker compose -f docker-compose.full.yml up -d --build

# 查看状态
docker compose -f docker-compose.full.yml ps

# 查看日志
docker compose -f docker-compose.full.yml logs -f web backend
```

访问：<http://localhost:8080/>

- 前端单文件 SPA 由 nginx 托管（hash 路由，无刷新 404）
- `/v0/management` 由 nginx 反代到后端容器（`backend:8317`）
- 前端默认自动探测同源 API，无需手动填地址

## 仅用官方后端镜像（无代理池二开 UI）

后端官方镜像 `eceasy/cli-proxy-api:latest` 不含代理池二开 UI，但后端 API 完整
（前端仍可用官方版 UI 连接）：

```bash
CLI_PROXY_IMAGE=eceasy/cli-proxy-api:latest \
docker compose -f docker-compose.full.yml up -d
```

## 常用环境变量

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `WEB_PORT` | `8080` | 前端宿主机端口 |
| `BACKEND_PORT` | `8317` | 后端监听端口（须与 config.yaml 的 `port` 一致） |
| `WEB_IMAGE` | `cli-proxy-web:local` | 前端镜像名 |
| `CLI_PROXY_IMAGE` | `cli-proxy-api:local` | 后端镜像名 |
| `CLI_PROXY_CONFIG_PATH` | `./config.yaml` | 后端配置挂载路径 |
| `CLI_PROXY_AUTH_PATH` | `./auths` | 认证目录挂载 |
| `CLI_PROXY_LOG_PATH` | `./logs` | 日志目录挂载 |
| `CLI_PROXY_PLUGIN_PATH` | `./plugins` | 插件目录挂载 |
| `VERSION` | `dev` | 构建版本号 |

## 常见问题

### 1. 前端页面打不开 / 白屏

- 确认 web 容器启动成功：`docker compose -f docker-compose.full.yml ps`
- 确认端口没被占用：`ss -ltnp | grep 8080`
- 前端是 hash 路由，刷新任意 `#/xxx` 页面不会 404

### 2. 管理 API 403 "remote management disabled"

后端只信任 `127.0.0.1`/`::1` 来源；nginx 反代来自 docker 网络，非 localhost，
必须设置 `remote-management.allow-remote: true`（见上 config.yaml 说明）。

### 3. 后端端口不是 8317

若 config.yaml 把 `port` 设为其他值（如 40010）：

```bash
BACKEND_PORT=40010 \
docker compose -f docker-compose.full.yml up -d --build
```

### 4. 后端健康检查失败

后端镜像 `HEALTHCHECK` 默认探测 `127.0.0.1:8317/healthz`，若端口不同需同步注入
`HEALTHCHECK_PORT`（compose 未自动注入时请在 environment 添加）。

### 5. 想直连后端管理 API（不经 nginx）

在 `backend` 服务的 `ports` 下加一行（取消注释即可）：

```yaml
    ports:
      - "8317:8317"
```

### 6. 前端如何连后端

前端 `src/services/api/client.ts` 默认用当前页面同源作为 API 地址：
`detectApiBaseFromLocation()` → `<页面 origin>/v0/management`。
nginx 已把 `/v0/` 反代到后端，因此**零配置**直连。若前端页面与后端不同源，
需在登录页手动填写 API 地址。

## 镜像说明

- **前端镜像**：`oven/bun:1` 构建（`bun install --frozen-lockfile` + `bun run build`
  产出单文件 `dist/index.html`，vite-plugin-singlefile 内联全部 JS/CSS），
  `nginx:1.27-alpine` 托管，nginx 模板注入反代目标。
- **后端镜像**：沿用仓库现有 `Dockerfile`（golang:1.26 多阶段构建），补充
  `curl` 与 `/healthz` HEALTHCHECK。

## 升级

```bash
docker compose -f docker-compose.full.yml build --pull
docker compose -f docker-compose.full.yml up -d
```