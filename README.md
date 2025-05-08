# NaiveProxy Caddy 定制编译

[![GitHub Release](https://img.shields.io/github/v/release/wekingchen/mycaddy?style=flat-square\&logo=github)](https://github.com/wekingchen/mycaddy/releases)
[![Build Status](https://img.shields.io/github/actions/workflow/status/wekingchen/mycaddy/build-caddy.yml?style=flat-square\&logo=github-actions)](https://github.com/wekingchen/mycaddy/actions)

基于 Caddy 官方镜像，集成 **forwardproxy (udpinhttp 分支)** 与多款扩展插件的定制化构建。通过 GitHub Actions 实现：

* **每日自动检测更新**，仅在上游代码发生变化时触发构建
* **双架构输出**：`amd64` 与 `arm64`
* **自动打包**：生成 `.tar.gz` 二进制包，便于下载与分发
* **版本格式**：`vX.Y.Z-YYYYMMDD-HHMMSS`，同时在 Release Notes 中记录对应 Commit

---

## 🌟 核心功能

| 插件名称                       | 功能描述                       |
| -------------------------- | -------------------------- |
| `forwardproxy`             | NaïveProxy 代理支持（udpinhttp） |
| `jsonc-adapter`            | JSONC 配置支持（保留注释）           |
| `caddy-l4`                 | TCP/UDP 第四层协议代理            |
| `caddy-dns/cloudflare`     | Cloudflare DNS 解析          |
| `caddy-dns/tencentcloud`   | 腾讯云 DNS 解析                 |
| `caddy-dns/duckdns`        | DuckDNS 动态域名解析             |
| `caddy-dynamicdns`         | 动态 DNS 更新                  |
| `caddy-events-exec`        | 事件触发自动化执行脚本                |
| `caddy-cloudflare-ip`      | Cloudflare IP 优选           |
| `caddy-trusted-cloudfront` | CloudFront 可信代理支持          |
| `caddy-webdav`             | WebDAV 文件服务器               |
| `caddy-trojan`             | Trojan 协议支持                |

---

## ⚡ 构建特性

* **自动同步**：每天 UTC 16:00（北京时间 00:00）检测 `imgk/forwardproxy:udpinhttp` 分支最新 Commit
* **智能触发**：仅当检测到上游代码变动或手动勾选 `force_build=true` 时才执行构建
* **多架构**：同时生成 `linux/amd64` 与 `linux/arm64` 可执行文件
* **自动打包**：使用 `file` 校验架构后，压缩为 `caddy_${arch}.tar.gz`
* **Release 发布**：

  * Tag 示例：`v2.6.4-20250508-000501`
  * Release Notes 中包含插件列表及 Commit 信息

---

## 🚀 快速开始

### 下载预编译包

1. 打开 [Releases 页面](https://github.com/wekingchen/mycaddy/releases)
2. 下载对应架构的压缩包：

   * `caddy_amd64.tar.gz`：适用于 Intel/AMD x86\_64 设备
   * `caddy_arm64.tar.gz`：适用于 ARM64 设备（如 Raspberry Pi / Apple Silicon）
3. 解压并运行：

   ```bash
   tar -xzf caddy_amd64.tar.gz    # 解压
   chmod +x caddy                  # 添加执行权限
   ./caddy version                 # 验证版本
   ```

### GitHub Actions 构建

1. 进入仓库的 **Actions** → 选择 **构建与发布 NaiveProxy Caddy**
2. 点击 **Run workflow**：

   * 若需强制全量编译，勾选输入框 `force_build=true`

---

## 🛠️ 本地编译指南

```bash
# 克隆 forwardproxy 源码 (udpinhttp 分支)
git clone --branch udpinhttp --depth 1 \
  https://github.com/imgk/forwardproxy.git forwardproxy

# 安装 xcaddy
go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
export PATH="$HOME/go/bin:$PATH"

# 编译 Caddy with 插件
xcaddy build \
  --output caddy \
  --with github.com/caddyserver/jsonc-adapter \
  --with github.com/mholt/caddy-l4 \
  --with github.com/caddy-dns/cloudflare \
  --with github.com/caddy-dns/tencentcloud \
  --with github.com/caddy-dns/duckdns \
  --with github.com/mholt/caddy-dynamicdns \
  --with github.com/mholt/caddy-events-exec \
  --with github.com/WeidiDeng/caddy-cloudflare-ip \
  --with github.com/xcaddyplugins/caddy-trusted-cloudfront \
  --with github.com/mholt/caddy-webdav \
  --with github.com/caddyserver/forwardproxy@master=./forwardproxy \
  --with github.com/imgk/caddy-trojan

# 校验架构并打包
file caddy                # 查看架构
tar -czf caddy.tar.gz caddy

# 运行示例
chmod +x caddy
./caddy version
```

---

## 📄 配置示例

### 基础代理配置

```caddy
:443 {
  forwardproxy {
    hide_ip
    hide_via
    probe_resistance
    basicauth user password
  }

  tls {
    protocols tls1.2 tls1.3
    curves x25519 secp521r1
  }
}
```

### 完整域名配置

```caddy
proxy.example.com {
  tls /path/to/cert.pem /path/to/key.pem

  forwardproxy {
    hide_ip
    hide_via
    probe_resistance
    basicauth admin secure_password
  }

  log {
    output file /var/log/caddy/access.log {
      roll_size 100MB
      roll_keep 5
    }
  }
}
```

---

## ⚠️ 注意事项

* 构建调度基于 UTC 16:00（北京时间 00:00）
* Release Notes 自动记录插件列表与对应 Commit
* 本地编译前请确保 Go 环境及 `xcaddy` 已安装
* 如需定制更多插件，可在 `xcaddy build` 命令中增删 `--with` 参数

---
