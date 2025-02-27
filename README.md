
```markdown
# NaiveProxy Caddy Build

[![GitHub Release](https://img.shields.io/github/v/release/wekingchen/mycaddy?style=flat-square)](https://github.com/wekingchen/mycaddy/releases)
[![Build Status](https://img.shields.io/github/actions/workflow/status/wekingchen/mycaddy/build-caddy.yml?style=flat-square)](https://github.com/wekingchen/mycaddy/actions)

自动构建集成 forwardproxy 及其他插件的 Caddy 服务器，每日同步上游更新。

## ✨ 功能特性

- ✅ 内置插件：
  - `forwardproxy` 核心支持（naive 分支）
  - Trojan 协议支持 (caddy-trojan)
  - 多 DNS 提供商 (Cloudflare/DNSPod/DuckDNS)
  - WebDAV 文件共享
  - 动态 DNS 更新
  - L4 网络层扩展
- 🚀 每日自动构建：
  - 检测上游仓库更新后触发
  - 同时编译 `amd64/arm64` 架构
  - 自动生成版本号 `naive-YYYYMMDD-HHMMSS`
- 📦 开箱即用：
  - GitHub Release 提供预编译二进制
  - 支持手动指定版本标签

## 📥 下载使用

1. 访问 [Releases 页面](https://github.com/wekingchen/mycaddy/releases)
2. 选择对应架构的二进制文件：
   - `caddy_amd64` - x86_64 架构
   - `caddy_arm64` - ARM64 架构
3. 赋予执行权限：
   ```bash
   chmod +x caddy_*
   ```
4. 验证版本信息：
   ```bash
   ./caddy_amd64 version
   ```

## ⚙️ 自定义构建

### 手动触发构建：
1. 进入仓库 Actions 页面
2. 选择 "Build and Release Caddy with Forwardproxy" 工作流
3. 点击 "Run workflow" 
4. 可选：输入自定义版本标签 (如 `v1.2.3-custom`)

### 本地构建提示：
```bash
# 克隆本仓库
git clone --branch naive --depth 1 https://github.com/wekingchen/mycaddy.git

# 进入目录执行构建
cd mycaddy
go run github.com/caddyserver/xcaddy/cmd/xcaddy build \
  --with github.com/caddyserver/forwardproxy@master=./forwardproxy \
  --with github.com/imgk/caddy-trojan
```

## 📄 配置文件示例

`Caddyfile` 基础配置模板：
```Caddyfile
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

## 📌 注意事项

- 每日自动构建基于 UTC 时间
- 使用 `secrets.GITHUB_TOKEN` 自动发布
- 遇到构建失败请检查 Actions 日志
- 建议通过 Releases 页下载验证过的版本

---

> ⚠️ 本项目仅用于技术研究，请遵守当地法律法规
```
