# Docker 部署（单容器直出，内置管理面板）

> 本方案已移除 nginx 前端容器：后端单容器直接对外服务，管理面板（自定义前端
> `Cli-Proxy-API-Management-Center` 构建的单文件 SPA）随镜像内置，访问
> `http://<host>:<端口>/management.html` 即可使用。

## 一、架构

```
浏览器 ──► 宿主机端口（.env 的 PORT_MAP，默认 13001）──► cli-proxy-api 容器 :8317
              │
              ├─ /management.html        内置管理面板（前端仓库 CI 产物）
              ├─ /v0/management/*        管理 API（登录密码 = secret-key）
              ├─ /v1/ /v1beta/ /openai/  OpenAI/Claude/Gemini 兼容代理 API
              └─ /v1/models              模型列表（用 api-keys 鉴权）
```

面板来源：`Dockerfile` 里的 `ARG WEB_IMAGE`，默认 `ghcr.io/qingdeng888/cli-proxy-web:latest`
（前端仓库 GitHub Actions 产物）。构建时该镜像内的 `index.html` 会被拷入后端镜像
`/CLIProxyAPI/static/management.html`，并通过 `MANAGEMENT_STATIC_PATH` 指向它，
配合 `config.yaml` 的 `disable-auto-update-panel: true`，运行时不会被上游面板覆盖。

## 二、最简启动

```bash
cd CLIProxyAPI

# 1. 配置（端口保持默认 8317，对外映射在 .env 改）
cp config.example.yaml config.yaml
#   编辑 config.yaml：
#     remote-management.secret-key     管理面板登录密码
#     remote-management.allow-remote   true（远程访问面板需要）
#     api-keys                         代理 API 密钥（保留模板值会触发 safe-mode 禁用代理端点）
#     disable-auto-update-panel        true（使用内置面板）

# 2. 对外端口（宿主机侧，容器内部端口不变）
echo "PORT_MAP=13001" > .env

# 3. 启动
docker compose up -d
```

访问：

- 管理面板：`http://<服务器IP>:13001/management.html`
- 模型列表：`curl http://<服务器IP>:13001/v1/models -H "Authorization: Bearer <你的api-key>"`
- OpenAI 兼容 base_url：`http://<服务器IP>:13001/v1`

## 三、镜像来源三选一

| 方式 | 命令 | 适用 |
|---|---|---|
| 直接用预构建后端镜像 | `docker compose up -d`（默认） | 最快，无需本地编译 |
| 本地构建后端（内置线上面板） | `docker compose build && docker compose up -d` | 改过后端源码 |
| 本地构建后端 + 本地构建面板 | 见下 | 前后端源码都在改（开发调试） |

本地构建面板并内置：

```bash
# 在 CLIProxyAPI 的上一级目录需存在前端仓库（clone qingdeng888/Cli-Proxy-API-Management-Center）
docker build -t cli-proxy-web:local -f ../Cli-Proxy-API-Management-Center/Dockerfile ../Cli-Proxy-API-Management-Center
docker build -t cli-proxy-api:local --build-arg WEB_IMAGE=cli-proxy-web:local .
CLI_PROXY_IMAGE=cli-proxy-api:local docker compose up -d --no-build
```

## 四、常用运维命令

```bash
docker compose ps                     # 状态
docker compose logs -f                # 日志
docker compose restart                # 重启
docker compose down                   # 停止（数据在挂载目录，不丢）
docker compose pull && docker compose up -d   # 升级到最新预构建镜像
```

配置修改：`config.yaml` 热加载生效（文件监视），改端口/挂载需 `docker compose up -d` 重建容器。

## 五、端口说明

- 对外端口只在 `docker-compose.yml` / `.env` 改：`"${PORT_MAP:-13001}:8317"`
- `config.yaml` 的 `port: 8317` 是容器内部端口，两者一致即可，一般不动

## 六、常见问题

### Q1：代理 API 返回 400 且提示 safe-mode
`api-keys` 还是模板值，后端自动禁用代理端点。登录面板改掉 api-keys 即恢复。

### Q2：面板登录 403 "remote management disabled"
`config.yaml` 里 `remote-management.allow-remote` 设为 `true` 后重启容器。

### Q3：面板看不到新功能（如代理池）
内置面板来自 `WEB_IMAGE`。确认前端仓库 CI 已构建新镜像（push 到 main 触发），再
`docker compose build --pull` 重建后端镜像；或直接 `docker compose pull` 用最新预构建后端。

### Q4：临时覆盖内置面板
运行时追加挂载即可：`docker run -v ./my-static:/CLIProxyAPI/static ...`
（挂载目录里有 `management.html` 时优先生效）。

## 七、文件清单

| 文件 | 作用 |
|---|---|
| `docker-compose.yml` | 单容器部署（本方案） |
| `docker-compose.cluster.yml` | 集群模式（可选） |
| `Dockerfile` | 后端镜像构建；`WEB_IMAGE` 参数指定内置面板来源 |
| `config.example.yaml` | 配置模板（生成你的 `config.yaml`） |
| `.env` | 端口/镜像等部署变量 |
