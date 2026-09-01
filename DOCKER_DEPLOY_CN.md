# Docker 一键部署（前后端完整系统）

> 本文面向 **完全没有 Docker 经验的小白**。跟着步骤做，就能把你的 CLIProxyAPI 后端 + 管理面板（Web UI）一起跑起来，不需要手动装 Go、Node、bun 这些环境。

---

## 一、这套部署是什么？

你的项目是两个仓库：

| 仓库 | 作用 |
| --- | --- |
| `CLIProxyAPI` | 后端服务（真正的代理服务器，提供 API） |
| `Cli-Proxy-API-Management-Center` | 前端网页（管理面板，浏览器里操作代理池、API Key 等） |

本方案用 **Docker 把两者编排在一起**，拓扑如下：

```
你打开浏览器
    │
    ▼
http://你的服务器IP:40010/
    │
    ├── /        → 前端网页 (nginx 容器里)
    └── /v0/     → 反向代理到后端 (cli-proxy-api 容器)
```

- 打开页面就是管理面板，**不需要单独配前端地址**（前端会自动探测同源 API）
- 只需要开放 **一个端口**（默认 `40010`），前端和后端都走它

---

## 二、准备工作

### 1. 安装 Docker（只装一次）

**Linux（Ubuntu/Debian）：**

```bash
curl -fsSL https://get.docker.com | sh
```

**Windows / macOS：** 安装 [Docker Desktop](https://www.docker.com/products/docker-desktop/)，安装后启动即可。

验证是否装好（能显示版本号就 OK）：

```bash
docker --version
docker compose version
```

### 2. 准备两个仓库（放在同级目录）

```
我的目录/
├── CLIProxyAPI/                      # 后端（本仓库）
└── Cli-Proxy-API-Management-Center/ # 前端管理面板
```

两个仓库必须是 **同级目录**（`docker-compose.full.yml` 里用 `../Cli-Proxy-API-Management-Center` 引用前端）。

```bash
git clone https://github.com/你的账号/CLIProxyAPI.git
git clone https://github.com/你的账号/Cli-Proxy-API-Management-Center.git
```

---

## 三、最简启动（5 分钟跑起来）

### 第 1 步：准备配置文件

进入后端仓库，复制示例配置：

```bash
cd CLIProxyAPI
cp config.example.yaml config.yaml
```

然后用编辑器打开 `config.yaml`，**至少修改两处**：

```yaml
# 1) 端口（默认 8317，改成你服务器开放的外网端口，比如 40010）
host: ""
port: 40010

# 2) 管理密码（打开面板要登录用的）
remote-management:
  allow-remote: true          # 必须改成 true！否则网页打不开管理接口
  secret-key: "你的登录密码"   # ← 改成你自己的密码
```

> **为什么 `allow-remote` 必须 `true`？**
> 网页(nginx)访问后端时，后端看到的来源 IP 是 Docker 网络内部的地址，不是本机 127.0.0.1。
> 后端为了安全默认只允许本机访问管理接口，所以 Docker 部署必须打开这个开关。

### 第 2 步：启动

**两种启动方式，选一种即可：**

**方式 A：预构建镜像（默认，推荐）** — 用 GitHub Actions 已经构建好的镜像，**不需要本地编译，秒级启动**：

```bash
docker compose -f docker-compose.full.yml up -d
```

**方式 B：本地构建镜像**（改了自己代码 / 没发布镜像时）：

```bash
docker compose -f docker-compose.full.local.yml up -d --build
```

> 方式 B 第一次运行会**自动构建镜像**（下载编译环境 + 编译，需要几分钟，耐心等）。
> 之后代码没变可以省略 `--build`：`docker compose -f docker-compose.full.local.yml up -d`

### 第 3 步：访问

浏览器打开：

```
http://你的服务器IP:40010/
```

登录页输入你刚才在 `config.yaml` 里设置的 `secret-key` 密码即可。

---

## 四、常用运维命令

> 以下命令的 `-f docker-compose.full.yml` 换成 `-f docker-compose.full.local.yml` 同样适用（本地构建版）。

```bash
# 查看运行状态（两个容器都应该是 Up 状态）
docker compose -f docker-compose.full.yml ps

# 查看日志（实时滚动，Ctrl+C 退出）
docker compose -f docker-compose.full.yml logs -f

# 只查看后端日志
docker compose -f docker-compose.full.yml logs -f backend

# 只查看前端日志
docker compose -f docker-compose.full.yml logs -f web

# 停止服务（不会删数据）
docker compose -f docker-compose.full.yml stop

# 重启服务
docker compose -f docker-compose.full.yml restart

# 彻底停止并删除容器（配置数据在挂载目录里，不会被删）
docker compose -f docker-compose.full.yml down
```

### 端口说明 / 常用环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `WEB_PORT` | `40010` | 前端（网页）对外的端口，浏览器访问这个端口 |
| `BACKEND_PORT` | `40010` | 后端内部监听端口（必须与 config.yaml 的 `port` 一致） |
| `WEB_IMAGE` | `ghcr.io/你的名/cli-proxy-web:latest` | 前端镜像（预构建版） |
| `CLI_PROXY_IMAGE` | `ghcr.io/你的名/cli-proxy-api:latest` | 后端镜像（预构建版） |
| `CLI_PROXY_CONFIG_PATH` | `./config.yaml` | config.yaml 挂载路径 |

改端口示例（把外网端口改成 9000）：

```bash
WEB_PORT=9000 BACKEND_PORT=40010 docker compose -f docker-compose.full.yml up -d
```

---

## 五、自定义配置（进阶）

### 修改密码

后端要求密码是 **bcrypt 哈希**（不是明文）。把明文密码变成哈希：

**方法一（推荐，用本项目自带 Go 环境）：**

```bash
cd CLIProxyAPI
cat > /tmp/genhash.go <<'EOF'
package main

import (
	"fmt"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	h, _ := bcrypt.GenerateFromPassword([]byte("你的新密码"), bcrypt.DefaultCost)
	fmt.Println(string(h))
}
EOF
go run /tmp/genhash.go
```

把输出的 `$2a$10$...` 复制到 `config.yaml` 的 `secret-key:`，然后：

```bash
docker compose -f docker-compose.full.yml up -d
```

后端启动时会重新读取 config.yaml（自动热加载），也可以用：

```bash
docker restart cli-proxy-api
```

### 修改代理池

代理池（本项目的二开功能）在 `config.yaml` 里：

```yaml
proxy-pool:
  - name: proxy-1
    url: http://user:pass@1.2.3.4:11189
    enabled: true
```

也可以在网页管理面板里点「代理池」页面增删改查（保存后后端自动热加载，不用重启容器）。

### 挂载目录说明

| 宿主机目录 | 容器内目录 | 作用 |
| --- | --- | --- |
| `./config.yaml` | `/CLIProxyAPI/config.yaml` | 后端配置 |
| `./auths` | `/root/.cli-proxy-api` | 认证文件（OAuth 登录的 Cookie 等） |
| `./logs` | `/CLIProxyAPI/logs` | 日志 |
| `./plugins` | `/CLIProxyAPI/plugins` | 插件 |

这些目录都在 `docker-compose.full.yml` 旁边的宿主机目录里，想备份直接拷贝目录即可。

---

## 六、常见问题（FAQ）

### Q1：打开网页白屏 / 连不上

```bash
# 1. 检查容器是否都在运行
docker compose -f docker-compose.full.yml ps

# 2. 看后端日志有没有报错
docker compose -f docker-compose.full.yml logs -f backend
```

如果容器是 `Restarting`，多半是后端启动失败。把日志贴出来排查。

### Q2：登录提示 403 "remote management disabled"

**原因**：`config.yaml` 里 `remote-management.allow-remote` 不是 `true`。

**解决**：改完后端会自动热重载；不生效就重启：

```bash
docker compose -f docker-compose.full.yml restart backend
```

### Q3：改了端口没生效

端口改了要两边一致：

1. `config.yaml` 的 `port` 改成新端口
2. 启动时用 `BACKEND_PORT=<新端口>` 覆盖 compose 里的默认值

```bash
BACKEND_PORT=12345 docker compose -f docker-compose.full.yml up -d
```

### Q4：密码忘了怎么办

直接改 `config.yaml` 的 `secret-key`（生成 bcrypt 哈希方法见上文），然后重启后端容器。

### Q5：日志太多 / 磁盘满了

```bash
# 查看占用
docker system df

# 清理不再使用的构建缓存
docker builder prune -f
```

### Q6：怎么升级到新版本

```bash
# 拉最新代码（触发 GitHub Actions 自动构建新镜像）
cd CLIProxyAPI && git pull
cd ../Cli-Proxy-API-Management-Center && git pull

# 预构建版：拉取最新镜像并重启（--pull 会强制重新拉取）
cd ../CLIProxyAPI
docker compose -f docker-compose.full.yml up -d --pull

# 本地构建版：重新编译
# docker compose -f docker-compose.full.local.yml up -d --build
```

---

## 七、文件清单

| 文件 | 位置 | 说明 |
| --- | --- | --- |
| `docker-compose.full.yml` | 后端仓库根目录 | 全栈编排（web + backend）**预构建镜像版（默认）** |
| `docker-compose.full.local.yml` | 后端仓库根目录 | 全栈编排 **本地构建版** |
| `Dockerfile` | 后端仓库根目录 | 后端镜像构建（Go 编译） |
| `Dockerfile` | 前端仓库根目录 | 前端镜像构建（bun 构建 → nginx） |
| `nginx/default.conf.template` | 前端仓库根目录 | nginx 配置模板（托管前端 + 反代 /v0/） |
| `.github/workflows/docker-build-push.yml` | 前后端仓库各自 | GitHub Actions：推 main 时自动构建镜像到 GHCR |

> **自动构建（GitHub Actions）**：两个仓库各自推 `main` 分支时，会自动构建并推送镜像到
> GitHub 容器仓库（GHCR）：`ghcr.io/<你的名>/cli-proxy-api` 和 `ghcr.io/<你的名>/cli-proxy-web`，
> 无需手动构建。GHCR 容器镜像**默认公开**，任何服务器免登录即可 `docker compose up` 拉取。
>
> 官方全量后端镜像：`eceasy/cli-proxy-api:latest`（后端单独用，不含前端）
> 若只想跑后端 + 用官方托管的前端页面，也可以直接 `docker run` 官方镜像：
> ```bash
> docker run -d --name cpa -p 40010:40010 \
>   -v $(pwd)/config.yaml:/CLIProxyAPI/config.yaml \
>   -v $(pwd)/logs:/CLIProxyAPI/logs \
>   eceasy/cli-proxy-api:latest
> ```