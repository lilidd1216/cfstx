# CFSTX 文档

**Cloudflare CDN 优选 IP 测速工具** · Windows 桌面端

基于 [XIU2/CloudflareSpeedTest](https://github.com/XIU2/CloudflareSpeedTest) 二次开发 · GPL-3.0

---

## 我是新用户

按这个顺序看：

1. **[快速开始](Getting-Started)** —— 下载哪个版本、怎么装、跑通第一次
2. **[优选IP 详解](Speed-Test)** —— 找出当前网络下最快的 Cloudflare IP
3. **[常见问题](FAQ)** —— 遇到问题先翻这里

---

## 按功能查

| 我想…… | 看这篇 |
|---|---|
| 测速、筛选 IP、导出结果 | [优选IP 详解](Speed-Test) |
| 解决「测速通、客户端 timeout」 | [优选IP → 自定义测速域名](Speed-Test#自定义测速域名) |
| 检测 ProxyIP、填进 edgetunnel | [ProxyIP 详解](ProxyIP) |
| 把 IP 写进 hosts，让浏览器直连也加速 | [优化工具 → Hosts 加速](Optimize-Tools#hosts-加速) |
| 用命令行跑测速 | [命令行手册](CLI) |
| 搞清楚测速原理、为什么结果会变 | [原理与限制](How-It-Works) |
| 自己编译、改代码 | [开发指南](Development) |

---

## 按症状查

| 症状 | 去哪 |
|---|---|
| 测速结果很好，客户端里大量 timeout | [FAQ](FAQ#测速结果很好但客户端里大量-timeout) |
| 可用 IP 很多，下载速度全是 0.00 | [FAQ](FAQ#可用-ip-很多但下载速度全是-000) |
| 延迟异常低（0.xx ms） | [FAQ](FAQ#延迟异常低比如-0xx-ms) |
| 一个 IP 都筛不出来 | [FAQ](FAQ#一个-ip-都筛不出来) |
| 测速返回 429 | [FAQ](FAQ#测速返回-429--全部-ip-不可用) |
| ProxyIP 显示「直连CF」，能用吗 | [ProxyIP 详解](ProxyIP#四种结果标记) |
| ProxyIP 显示「待验证」，能用吗 | [ProxyIP 详解](ProxyIP#四种结果标记) |
| Hosts 写入失败 / 提示需要管理员 | [FAQ](FAQ#hosts-写入失败) |
| 安装包提示「无法验证发布者」 | [FAQ](FAQ#安装时提示无法验证发布者) |

---

## 核心概念速查

| 名词 | 一句话解释 |
|---|---|
| **优选 IP** | 客户端连接 Cloudflare 边缘时用的入口地址，影响「你的设备 → CF」这一段 |
| **ProxyIP** | Worker 回源时用的第三方中转服务器，影响「Worker → CF」这一段 |
| **直连 CF** | 出口 IP 等于你自己公网 IP 的地址 —— 中间没有中转，只是入口 |
| **域名探测** | 用你的域名请求 `/cdn-cgi/trace`，只保留真正服务该域名的 IP |
| **colo** | Cloudflare 边缘机房代码，如 `HKG`（香港）、`LAX`（洛杉矶） |

---

## 项目链接

- **仓库**：https://gitee.com/liliaic/cfstx
- **Releases**：https://gitee.com/liliaic/cfstx/releases
- **问题反馈**：https://gitee.com/liliaic/cfstx/issues
- **更新日志**：[Changelog](Changelog)
- **上游项目**：https://github.com/XIU2/CloudflareSpeedTest

---

> [!IMPORTANT]
> Cloudflare CDN 已**明文禁止代理**方式使用。通过代理套 CDN 的请自行承担风险，请勿过度依赖。
