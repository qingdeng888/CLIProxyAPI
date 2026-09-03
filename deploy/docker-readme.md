# 单容器 Docker 部署（CLIProxyAPI，内置 Web UI）

本 README 说明如何用 Docker 部署后端（CLIProxyAPI）。管理面板
（Cli-Proxy-API-Management-Center 构建的单文件 SPA）在镜像构建时内置，
后端单容器直出，与 API 同源访问、无需 nginx、无需配置跨域。

部署文件分布（全部在后端仓库根目录）：

| 文件 | 说明 |
| --- | --- |
| `docker-compose.yml` | 单容器编排（面板内置于镜像，无额外挂载） |
| `Dockerfile` | 后端镜像；`ARG WEB_IMAGE` 指定内置面板来源 |
| `.env` / `.env.example` | 部署变量（对外端口 `PORT_MAP`、镜像名等） |

面板来源：前端仓库 CI 构建的 `ghcr.io/qingdeng888/cli-proxy-web:latest`
（镜像内 `/usr/share/nginx/html/index.html` 即单文件 SPA）。后端 Dockerfile 用
`COPY --from=web` 将其取入镜像 `/CLIProxyAPI/static/management.html`，并设
`MANAGEMENT_STATIC_PATH=/CLIProxyAPI/static` 指向它。

## 快速开始

```bash
cd CLIProxyAPI

# 准备 config.yaml（后端配置，含管理密钥）
cp config.example.yaml config.yaml
#   编辑 config.yaml：
#   - port: 8317                      # 默认即可（容器内部端口）
#   - remote-management:
#       allow-remote: true            # 非 localhost 访问管理 API 必须开启
#       secret-key: <你的管理密钥>     # 面板登录密码
#       disable-auto-update-panel: true  # 使用内置面板，禁止被上游面板覆盖
#   - api-keys: [<你的代理 API 密钥>]  # 保留模板值会触发 safe-mode 禁用代理端点

# 对外端口（宿主机侧）
echo "PORT_MAP=8080" > .env

docker compose up -d
```

访问：<http://localhost:8080/management.html>

- 面板与 API 同容器同源，前端自动探测同源 API，**零配置**直连
- OpenAI 兼容 API：`http://localhost:8080/v1`（`/v1/models`、`/v1/chat/completions`）
- 管理 API：`/v0/management/*`（Bearer <secret-key>）

## 仅用官方后端镜像（不含二开面板）

官方镜像 `eceasy/cli-proxy-api:latest` 不含代理池二开 UI（后端会下载上游官方面板，
功能子集）：

```bash
CLI_PROXY_IMAGE=eceasy/cli-proxy-api:latest docker compose up -d
```

注意：该镜像不带 `disable-auto-update-panel` 配置的话，面板来自上游；若要二开 UI，
用本仓库构建的镜像（默认 `CLI_PROXY_IMAGE` 即指向 CI 产物 `ghcr.io/<owner>/cli-proxy-api`）。

## 本地构建（前端源码同步调试）

```bash
# 同级目录 clone 前端仓库，构建本地面板镜像
docker build -t cli-proxy-web:local -f ../Cli-Proxy-API-Management-Center/Dockerfile ../Cli-Proxy-API-Management-Center

# 构建后端镜像并内置本地面板
docker build -t cli-proxy-api:local --build-arg WEB_IMAGE=cli-proxy-web:local .

# 运行
CLI_PROXY_IMAGE=cli-proxy-api:local docker compose up -d --no-build
```

## 常用环境变量

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `PORT_MAP` | `13001` | 对外宿主机端口（浏览器访问） |
| `CLI_PROXY_IMAGE` | `eceasy/cli-proxy-api:latest` | 后端镜像名 |
| `WEB_IMAGE` | `ghcr.io/qingdeng888/cli-proxy-web:latest` | 构建期面板来源镜像 |
| `CLI_PROXY_CONFIG_PATH` | `./config.yaml` | 后端配置挂载路径 |
| `CLI_PROXY_AUTH_PATH` | `./auths` | 认证目录挂载 |
| `CLI_PROXY_LOG_PATH` | `./logs` | 日志目录挂载 |
| `CLI_PROXY_PLUGIN_PATH` | `./plugins` | 插件目录挂载 |
| `VERSION` | `dev` | 构建版本号 |

## 常见问题

### 1. 页面打不开 / 白屏

- 确认容器运行：`docker compose ps`（状态应为 `Up (healthy)`）
- 确认端口没被占用：`ss -ltnp | grep 13001`
- 前端是 hash 路由，刷新任意 `#/xxx` 页面不会 404
- 必须带路径访问：`/management.html`（根路径 `/` 不是面板）

### 2. 管理 API 403 "remote management disabled"

非 localhost 来源访问管理端点必须设置 `remote-management.allow-remote: true`。

### 3. 代理 API 返回 400 且提示 safe-mode

`api-keys` 含模板值（`your-api-key-*`），后端自动禁用代理端点。
登录面板修改 api-keys 即恢复（热加载）。

### 4. 模型列表为空 / "未从 /models 获取到模型数据"

面板请求同源 `${apiBase}/v1/models`。单容器直出模式下天然可达；若仍为空，
检查：添加的上游服务商凭证是否加载成功（日志 `full client load complete`）、
api-keys 是否非模板值。

### 5. 面板显示的不是二开版本（看不到代理池）

构建时 `WEB_IMAGE` 未取到最新面板镜像。升级：

```bash
docker compose build --pull   # 重新拉取最新 WEB_IMAGE 并构建
docker compose up -d
```

### 6. 临时覆盖内置面板

运行时追加挂载 `-v ./my-static:/CLIProxyAPI/static`（目录内含 `management.html`），
`MANAGEMENT_STATIC_PATH` 优先读挂载目录。

### 7. 后端健康检查失败

后端镜像 `HEALTHCHECK` 默认探测 `127.0.0.1:8317/healthz`，若 config.yaml 的
`port` 改为其他值，需同步注入 `HEALTHCHECK_PORT`。

## 镜像说明

- **面板内置**：前端仓库 CI 产出 `cli-proxy-web:latest`（bun 构建单文件
  `dist/index.html`，vite-plugin-singlefile 内联全部 JS/CSS；镜像本身仍是
  nginx 基座，但后端构建只 `COPY --from=web` 取其中产物，不运行它）。
- **后端镜像**：`Dockerfile` 多阶段构建（golang:1.26 → debian:bookworm），
  `FROM ${WEB_IMAGE} AS web` 阶段引入面板，最终镜像含后端 + 面板 + `/healthz`。

## 升级

```bash
docker compose pull && docker compose up -d          # 预构建镜像路线
docker compose build --pull && docker compose up -d  # 本地构建路线（同时刷新面板）
```
