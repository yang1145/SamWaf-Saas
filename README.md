<div align="center">
 <img alt="SamWaf" width="100" src="./docs/images/logo.png">

**SamWaf-Saas** — 开源轻量级网站应用防火墙（多租户 SaaS 版）

仓库地址：<https://github.com/yang1145/SamWaf-Saas>（前端：<https://github.com/yang1145/SamWafWeb-Saas>）

![License](https://img.shields.io/badge/License-Apache%202.0-blue?style=flat-square)
![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?style=flat-square&logo=go)
![Vue](https://img.shields.io/badge/Vue-3-4FC08D?style=flat-square&logo=vue.js)
</div>

---

## 项目简介

本项目是 SamWaf 的多租户 SaaS 改造版：在保留原版全部防护能力（反向代理、OWASP CRS、CC 防护、SSL 证书管理等）的基础上，新增了完整的**共享库多租户体系**：

- **租户隔离**：请求级租户上下文贯穿全部数据读写，站点/规则/日志/证书等按租户严格隔离，引擎数据面全租户加载
- **权限组**：在原有三权分立（超级/系统/安全/审计管理员）之上，支持租户级自定义角色（8 个域级权限码自由组合）
- **租户生命周期**：开通（自动复制预置权限组 + 创建租户管理员）、续期、停用/注销、到期宽限与只读策略、站点/账号配额
- **平台扩展点**：OpenAPI Key 授权范围、平台域/账号域分离，为计费系统对接预留接口

数据库三选一：加密 SQLite（默认，零依赖）/ MySQL / PostgreSQL，多租户生产环境推荐 MySQL 或 PostgreSQL。

> 多租户改造的设计、机制与安全审计全文见 [docs/saas_multitenant_phase0.md](docs/saas_multitenant_phase0.md)。

## 架构

![SamWaf架构](./docs/images/tecDesign.svg)

## 主要功能

### 多租户与权限（本版新增）
- 共享库多租户：数据行级隔离 + 请求级租户上下文，白盒隔离测试矩阵 + 越权端到端回归覆盖
- 权限组：自定义角色（仪表盘/站点/日志/防护策略/审计/系统运维/账号/平台管理 8 域权限码）
- 租户生命周期：开通即初始化（预置权限组副本 + 租户管理员）、续期、到期宽限只读、配额（站点数/账号数）
- 三权分立保留：superAdmin / systemAdmin / securityAdmin / auditAdmin，存量账号行为完全兼容
- OpenAPI Key 授权范围：按权限码限定 Key 可调用的接口集合

### 基础特性
- 代码完全开源（Apache 2.0）
- 数据加密保存、通讯加密，默认内置加密 SQLite，可选 MySQL / PostgreSQL
- 完全独立引擎，防护不依赖 IIS、Nginx
- 支持 IPv6、HTTP/1.1、HTTP/2、HTTP/3（QUIC）

### 流量接入
- 反向代理与负载均衡（加权轮询、IP Hash、最小连接数），后端健康检查自动摘除
- 路径规则、静态站点伺服、301/302 重定向、TCP/UDP 四层隧道转发
- 网页缓存加速、HTTP Basic Auth / 统一访问认证、WebSocket 转发

### 攻击防护
- SQL 注入 / XSS / RCE / 扫描器 / 目录穿越 / 文件上传检测
- CC 频率限制、虚假爬虫识别、防盗链、CSRF 防护、敏感词过滤
- OWASP CRS 规则集（Coraza 引擎）、自定义防护规则、人机验证、仅记录模式

### 访问控制与数据安全
- IP/URL 黑白名单、地区封禁、联动系统防火墙
- 日志加密、信息脱敏（DLP）、网页防篡改、Cookie 加固

### SSL 证书
- ACME 自动申请续签（多 CA 与 EAB）、SNI 多证书、批量到期检测

### 运维管理
- 账号 RBAC + OTP 双因素、登录/操作日志、安全审计
- 数据保留策略与日志分片归档、在线升级、定时/批量任务
- 通知告警：邮件、钉钉、飞书、企业微信、Server酱、Webhook、Kafka、RabbitMQ

---

## 编译安装（唯一安装方式）

本项目**不提供预编译下载**，统一从源码编译。管理端前端（`SamWafWeb-Saas`）构建产物会内嵌进后端二进制，因此**必须先构建前端、再编译后端**。

### 环境要求

| 组件 | 版本要求 | 用途 |
|------|---------|------|
| Go | ≥ 1.25 | 后端编译 |
| GCC | 任意近期版本（Linux 装 `build-essential`，Windows 装 mingw-w64） | CGO（加密 SQLite 驱动必需） |
| Node.js + npm | ≥ 18 | 管理端前端构建 |
| Git | 任意近期版本 | 拉取代码 |

### 第一步：获取代码

```bash
git clone https://github.com/yang1145/SamWaf-Saas.git
git clone https://github.com/yang1145/SamWafWeb-Saas.git
```

### 第二步：构建管理端前端

```bash
cd SamWafWeb-Saas
npm install
npm run build            # 产物在 dist/ 目录
```

### 第三步：同步前端产物到后端

```bash
cd ../SamWaf-Saas
rm -rf public/dist
cp -r ../SamWafWeb-Saas/dist public/dist
```

> 后端通过 `go:embed` 将 `public/dist` 内嵌进二进制（`public/public.go`）。跳过此步会使用仓库内置的旧版前端。

### 第四步：编译后端

```bash
CGO_ENABLED=1 go build -ldflags "-s -w -X SamWaf/global.GWAF_RELEASE=true" -o SamWafLinux64 ./cmd/samwaf
```

`GWAF_RELEASE=true` 会启用内嵌前端静态资源服务；不加该参数仅影响管理端页面托管，引擎功能不受影响。

Windows 下编译（需 mingw-w64 的 gcc 在 PATH 中）：

```bat
set CGO_ENABLED=1
go build -ldflags "-s -w -X SamWaf/global.GWAF_RELEASE=true" -o SamWaf64.exe ./cmd/samwaf
```

### 第五步：首次启动

```bash
./SamWafLinux64
```

- 管理端地址：`http://<服务器IP>:26666`
- 全新安装会自动生成随机初始管理员口令，保存在 `data/initial_password.txt`，首次登录后请立即修改

---

## Linux 部署方案（生产推荐）

以下为 Ubuntu/Debian 上的完整部署流程（CentOS/RHEL 将包管理命令替换为 yum/dnf 即可）。

### 1. 安装编译环境

```bash
sudo apt update
sudo apt install -y build-essential git curl
# Go 1.25+（官方 tarball，或使用系统源中的 golang-go）
wget https://go.dev/dl/go1.25.9.linux-amd64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.25.9.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/go.sh && source /etc/profile.d/go.sh
# Node.js 20（NodeSource 源）
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
sudo apt install -y nodejs
```

### 2. 编译（见上文第一至第四步，Linux 原生编译无需交叉工具链）

```bash
git clone https://github.com/yang1145/SamWaf-Saas.git /opt/SamWaf-Saas
git clone https://github.com/yang1145/SamWafWeb-Saas.git /opt/SamWafWeb-Saas
cd /opt/SamWafWeb-Saas && npm install && npm run build
cd /opt/SamWaf-Saas
rm -rf public/dist && cp -r /opt/SamWafWeb-Saas/dist public/dist
CGO_ENABLED=1 go build -ldflags "-s -w -X SamWaf/global.GWAF_RELEASE=true" -o /opt/samwaf/SamWafLinux64 ./cmd/samwaf
```

### 3. 数据库准备（多租户生产环境推荐 MySQL，二选一）

**MySQL 8.x：**

```sql
CREATE DATABASE samwaf_core CHARACTER SET utf8mb4;
CREATE DATABASE samwaf_log  CHARACTER SET utf8mb4;
CREATE DATABASE samwaf_stats CHARACTER SET utf8mb4;
CREATE USER 'samwaf'@'localhost' IDENTIFIED BY '你的强密码';
GRANT ALL PRIVILEGES ON samwaf_core.* TO 'samwaf'@'localhost';
GRANT ALL PRIVILEGES ON samwaf_log.*  TO 'samwaf'@'localhost';
GRANT ALL PRIVILEGES ON samwaf_stats.* TO 'samwaf'@'localhost';
FLUSH PRIVILEGES;
```

**PostgreSQL 16.x**：首次连接会自动建库，只需准备好角色与口令。

### 4. 配置文件

在部署目录创建 `conf/config.yml`（首次启动会自动生成默认配置，随后修改重启即可）：

```yaml
database:
  driver: mysql            # sqlite（默认）| mysql | postgres
  mysql:
    host: 127.0.0.1
    port: 3306
    user: samwaf
    password: "你的强密码"
    core_db: samwaf_core
    log_db: samwaf_log
    stats_db: samwaf_stats
```

选择 PostgreSQL 时对应 `database.postgres.*`（host/port/user/password/sslmode 等）。全部启动后租户表、权限组表及预置数据会自动迁移初始化。

### 5. systemd 服务（开机自启 + 崩溃拉起）

```ini
# /etc/systemd/system/samwaf.service
[Unit]
Description=SamWaf Web Application Firewall
After=network.target mysql.service
Wants=mysql.service

[Service]
Type=simple
User=samwaf
WorkingDirectory=/opt/samwaf
ExecStart=/opt/samwaf/SamWafLinux64
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```

```bash
sudo useradd -r -s /usr/sbin/nologin samwaf
sudo chown -R samwaf:samwaf /opt/samwaf
sudo systemctl daemon-reload
sudo systemctl enable --now samwaf
sudo systemctl status samwaf
```

### 6. 防火墙端口

| 端口 | 用途 | 建议 |
|------|------|------|
| 26666/tcp | 管理端 | 生产环境务必配置管理端 IP 白名单 + HTTPS（系统配置内开启） |
| 80/443/tcp | 站点反代流量 | 按需开放 |
| 隧道端口 | TCP/UDP 四层转发 | 按需开放 |

### 7. 部署验证

```bash
systemctl is-active samwaf                       # active
cat /opt/samwaf/data/initial_password.txt        # 初始管理员口令
curl -I http://127.0.0.1:26666/                  # 管理端可达
# MySQL 侧确认迁移完成（多租户表已建、预置权限组已播种）
mysql -u samwaf -p samwaf_core -e "SELECT id FROM migrations ORDER BY id DESC LIMIT 3; SELECT group_name FROM permission_groups; SELECT tenant_id FROM tenants;"
```

浏览器打开 `http://<IP>:26666`，用初始口令登录并立即修改密码。

### 8. 升级

```bash
cd /opt/SamWaf-Saas && git pull
cd /opt/SamWafWeb-Saas && git pull && npm install && npm run build
cp -r dist /opt/SamWaf-Saas/public/dist
cd /opt/SamWaf-Saas && CGO_ENABLED=1 go build -ldflags "-s -w -X SamWaf/global.GWAF_RELEASE=true" -o /opt/samwaf/SamWafLinux64 ./cmd/samwaf
sudo systemctl restart samwaf
```

升级过程会自动执行数据库增量迁移（支持回滚），建议先在测试环境验证。

---

## Windows 编译部署

安装 mingw-w64 并加入 PATH，其余步骤与上同（`SamWaf64.exe`）。可直接以服务方式运行：

```
SamWaf64.exe install && SamWaf64.exe start     # 安装并启动服务
SamWaf64.exe stop && SamWaf64.exe uninstall    # 停止并卸载
```

---

## 命令行工具

| 命令 | 说明 |
|------|------|
| `install` / `uninstall` | 安装 / 卸载系统服务 |
| `start` / `stop` / `restart` | 启动 / 停止 / 重启服务 |
| `rolling-restart` | 零停机滚动重启 |
| `resetpwd` | 重置管理员密码 |
| `resetotp` | 重置安全码（OTP） |
| `repairdb` | 修复损坏的数据库 |
| `execsql` | 在指定数据库上执行 SQL |
| `migratedb` | 离线迁移 SQLite → MySQL / PostgreSQL |
| `rollback` | 回退到历史备份版本 |

---

## 多租户与安全

- 多租户改造的设计文档、隔离机制、审计结论：[docs/saas_multitenant_phase0.md](docs/saas_multitenant_phase0.md)
- 全表租户隔离测试矩阵（52 模型 × SQLite/MySQL/PostgreSQL）、越权端到端回归、绕过通道审计均已纳入 CI 可跑范围
- 安全策略：[SECURITY.md](SECURITY.md)

## 测试效果

[测试效果](./test/attackTest.md)

## 问题反馈

- 本仓库 Issues：<https://github.com/yang1145/SamWaf-Saas/issues>

## 许可证

Apache 2.0，详见 [LICENSE](LICENSE)；第三方组件声明见 [ThirdLicense](ThirdLicense)。

## 致谢

基于开源项目 [samwafgo/SamWaf](https://github.com/samwafgo/SamWaf) 演进，感谢原项目作者与贡献者。
