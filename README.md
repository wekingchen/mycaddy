# NaiveProxy Caddy 定制编译

[![GitHub Release](https://img.shields.io/github/v/release/wekingchen/mycaddy?style=flat-square&logo=github)](https://github.com/wekingchen/mycaddy/releases)
[![Build Status](https://img.shields.io/github/actions/workflow/status/wekingchen/mycaddy/build-caddy.yml?branch=main&style=flat-square&logo=github-actions)](https://github.com/wekingchen/mycaddy/actions)

每日自动构建集成 **forwardproxy 及多款扩展插件的 Caddy 服务器**，支持手动触发编译与跨平台部署。

---

## 🌟 核心功能

### 🧩 内置插件集
| 插件名称                      | 功能描述                   |
|-------------------------------|--------------------------|
| `forwardproxy`                | NaïveProxy 代理支持       |
| `caddy-l4`                    | TCP/UDP 四层代理          |
| `caddy-dns/cloudflare`        | Cloudflare DNS 解析       |
| `caddy-dns/dnspod`            | DNSPod 域名解析           |
| `caddy-dynamicdns`            | 动态 DNS 更新服务         |
| `caddy-events-exec`           | 事件触发自动化脚本         |
| `caddy-cloudflare-ip`         | Cloudflare IP 解析优化    |
| `caddy-trusted-cloudfront`    | CloudFront 可信代理支持   |
| `caddy-webdav`                | WebDAV 文件服务器         |
| `caddy-trojan`                | Trojan 协议支持           |

### ⚡ 构建特性
- **智能编译策略**
  - 🕒 每日 UTC 时间自动同步上游更新
  - 🔄 仅代码变更时触发构建，节省资源
  - 🏗️ 同时生成 `amd64` / `arm64` 双架构版本
  - 📌 版本号格式：`vX.Y.Z-YYYYMMDD-HHMMSS`

- **便捷部署**
  - 📦 GitHub Release 提供预编译二进制
  - 🖥️ 开箱即用，无需复杂配置
  - 📥 支持 Linux 系统直接运行

---

## 🚀 快速开始

### 下载预编译版本
1. 访问 [Releases 页面](https://github.com/wekingchen/mycaddy/releases)
2. 根据架构选择：
   - `caddy_amd64`：x86_64 设备（Intel/AMD 处理器）
   - `caddy_arm64`：ARM64 设备（树莓派/Apple Silicon）
3. 终端操作：
   ```bash
   chmod +x caddy_*  # 添加执行权限
   ./caddy_amd64 version  # 验证版本

### 自定义构建
#### GitHub Actions 编译
1. 访问仓库的 Actions 页面
2. 选择 `Build and Release Caddy with Forwardproxy` 工作流
3. 点击 `Run workflow` 触发构建
   - ✅ 强制编译：勾选 `force_build=true`

#### 本地编译指南
```bash
# 获取 forwardproxy 源码
git clone --branch naive --depth 1 \
  https://github.com/klzgrad/forwardproxy.git

# 安装构建工具
go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
export PATH="$HOME/go/bin:$PATH"

# 执行编译命令
xcaddy build \
  --with github.com/caddyserver/forwardproxy@master=./forwardproxy \
  --with github.com/mholt/caddy-l4 \
  --with github.com/caddy-dns/cloudflare \
  --with github.com/caddy-dns/dnspod \
  --with github.com/caddy-dns/duckdns \
  --with github.com/mholt/caddy-dynamicdns \
  --with github.com/mholt/caddy-events-exec \
  --with github.com/WeidiDeng/caddy-cloudflare-ip \
  --with github.com/xcaddyplugins/caddy-trusted-cloudfront \
  --with github.com/mholt/caddy-webdav \
  --with github.com/imgk/caddy-trojan

---

## 🛠️ 配置示例

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
- 自动构建基于 UTC 时间（北京时间 08:00）
- 使用 `GITHUB_TOKEN` 实现自动发布流程
- 生产环境建议使用 Releases 中的稳定版本
- 所有操作需遵守当地法律法规
