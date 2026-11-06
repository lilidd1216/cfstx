# CFSTX - 配套脚本

> 本目录脚本源自 [XIU2/CloudflareSpeedTest](https://github.com/XIU2/CloudflareSpeedTest) 的脚本目录，已移除 Linux/macOS 版本并适配 CFSTX，**感谢原作者 [XIU2](https://github.com/XIU2)**。

这里提供几个 Windows 批处理脚本，通过调用 `cfstx.exe` 测速，并按需处理测速结果（更新 Hosts、3Proxy 配置、DDNS 解析等）。

## 使用前提

1. 将 `cfstx.exe`、`ip.txt` 与本目录脚本放在**同一文件夹**
2. 涉及修改系统配置的脚本需**以管理员身份运行**
3. 脚本中的测速参数可按需自行修改

---

## 📑 cfstx_hosts.bat

运行 CFSTX 获取最快 IP，替换 Hosts 文件中的旧 Cloudflare CDN IP。

- 需要管理员权限
- 使用说明：[#42](https://github.com/XIU2/CloudflareSpeedTest/issues/42#issuecomment-768273768)

## 📑 cfstx_3proxy.bat

运行 CFSTX 测速后获取最快 IP，替换 3Proxy 配置文件中的旧 Cloudflare CDN IP。

可以把所有 Cloudflare CDN IP 都重定向至最快 IP，实现一次性加速所有使用 Cloudflare CDN 的网站（无需逐个添加域名到 Hosts）。

- 需要先安装并配置 [3Proxy](https://github.com/3proxy/3proxy)
- 使用说明：[#71](https://github.com/XIU2/CloudflareSpeedTest/discussions/71)

## 📑 cfstx_ddns.bat

如果你的域名托管在 **Cloudflare**，可通过 Cloudflare 官方 API 自动更新域名解析记录为最快 IP。

- 支持 API 令牌方式（相比全局 API 密钥更安全）
- 使用说明：[#481](https://github.com/XIU2/CloudflareSpeedTest/discussions/481)

---

## 问题反馈

脚本使用中遇到问题，建议先查阅对应的上游讨论帖。仍无法解决时，可在本仓库提交 Issue：

<https://gitee.com/liliaic/cfstx/issues>

## 致谢

- 脚本原作者：[**XIU2**](https://github.com/XIU2)
- 原始项目：[https://github.com/XIU2/CloudflareSpeedTest](https://github.com/XIU2/CloudflareSpeedTest)

本项目遵循 GPL-3.0 许可证，脚本内容保留原作者的版权与署名。
