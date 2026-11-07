# 命令行手册

同一个 `cfstx.exe`，**带参数运行即进入命令行模式**，不带参数进入图形界面。

---

## 什么时候用命令���

| 场景 | 适合 |
|---|---|
| 定时任务、开机脚本 | ✅ |
| 服务器 / 无桌面环境（Wine 等） | ✅ |
| 把结果自动喂给下游脚本 | ✅ |
| 第一次用、想看清每个参数 | ❌ 用图形界面，参数说明更直观 |
| 需要 ProxyIP 检测 | ❌ 仅图形界面支持 |

> **注意**：ProxyIP 检测、Hosts 写入、应用内更新目前**只在图形界面提供**。

---

## 参数总览

```text
CFSTX vX.X.X
测试各个 CDN 或网站所有 IP 的延迟和速度，获取最快 IP (IPv4+IPv6)！
https://gitee.com/liliaic/cfstx

参数：
    -n 200
        延迟测速线程；越多延迟测速越快，性能弱的设备请勿太高；(默认 200 最多 1000)
    -t 4
        延迟测速次数；单个 IP 延迟测速的次数；(默认 4 次)
    -dn 10
        下载测速数量；延迟测速并排序后，从最低延迟起下载测速的数量；(默认 10 个)
    -dt 10
        下载测速时间；单个 IP 下载测速最长时间；(默认 10 秒)
    -tp 443
        指定测速端口；列表项没有自带端口时使用的默认端口；(默认 443)
    -url https://speed.cloudflare.com/__down?bytes=90000000
        指定测速地址；延迟测速(HTTPing)/下载测速时使用的地址
        （该端点单次请求上限 100MB，bytes 超过会返回 403）

    -httping
        切换测速模式；延迟测速模式改为 HTTP 协议；(默认 TCPing)
    -httping-code 200
        有效状态代码；HTTPing 延迟测速时网页返回的有效 HTTP 状态码，仅限一个；
        (默认 200 301 302)
    -cfcolo HKG,KHH,NRT,LAX,SEA,SJC,FRA,MAD
        匹配指定地区；IATA 机场地区码或国家/城市码，英文逗号分隔，仅 HTTPing 模式可用

    -tl 200
        平均延迟上限；只输出低于指定平均延迟的 IP；(默认 9999 ms)
    -tll 40
        平均延迟下限；只输出高于指定平均延迟的 IP；(默认 0 ms)
    -tlr 0.2
        丢包几率上限；范围 0.00~1.00，0 过滤掉任何丢包的 IP；(默认 1.00)
    -sl 5
        下载速度下限；只输出高于指定下载速度的 IP；(默认 0.00 MB/s)

    -p 10
        显示结果数量；为 0 时不显示结果直接退出；(默认 10 个)
    -f ip.txt
        IP段数据文件；如路径含有空格请加上引号；(默认 ip.txt)
    -ip 1.1.1.1,2.2.2.2/24,2606:4700::/32
        指定IP段数据；英文逗号分隔；(默认 空)
    -o result.csv
        写入结果文件；值为空时不写入文件 [-o ""]；(默认 result.csv)

    -bestcf
        在线优选IP；从 bestcf.pages.dev 的「优选IP BestIP」区块抓取全部 IP 并合并测速
    -official
        Cloudflare官方IP段；从 api.cloudflare.com 获取官方发布的网段并展开测速
    -cmliu
        cmliu的CF-CIDR；使用社区维护的 Cloudflare 网段列表
    -probe fast3.example.com
        自定义测速域名；延迟测速前先用 <域名>/cdn-cgi/trace 探测哪些 IP 真正服务该域名，
        自动剔除不适配的 IP（这类 IP 在客户端里会表现为大量连接超时）；
        该路径由 Cloudflare 边缘节点直接响应，不消耗 Workers 请求配额

    -dd
        禁用下载测速；禁用后测速结果会按延迟排序 (默认按下载速度排序)
    -allip
        测速全部的IP；对 IP 段中的每个 IP (仅支持 IPv4) 进行测速；
        (默认 每个 /24 段随机测速一个 IP)

    -debug
        调试输出模式；会在一些非预期情况下输出更多日志以便判断原因
    -v
        打印程序版本 + 检查版本更新
    -h
        打印帮助说明
```

> 所有参数都有默认值，**顺序无关**。

---

## 常用示例

### 只要延迟最快的 10 个

```bat
cfstx -dd -tl 200
```

- `-dd` 禁用下载测速
- `-tl 200` 只保留延迟低于 200ms 的

### Worker 专用：探测自己的域名

```bat
cfstx -probe fast3.example.com -dd
```

只保留真正服务 `fast3.example.com` 的 IP。

### 组合多个在线来源

```bat
cfstx -bestcf -official -cmliu -tl 250 -sl 5
```

合并三个在线来源，筛出延迟 <250ms 且下载 >5MB/s 的。

### 按机房筛选（只要香港、日本、新加坡）

```bat
cfstx -httping -cfcolo HKG,NRT,SIN -dd
```

### 只测指定的 IP

```bat
cfstx -ip 104.16.0.1,104.17.0.0/24 -dd
```

### 结果输出到指定文件

```bat
cfstx -o D:\cfstx\result.csv
```

### 指定测速端口

```bat
cfstx -tp 8443
```

只影响**列表项没有自带端口**的情况。

### 全部 IP 都测（慢）

```bat
cfstx -allip -dd
```

对 IP 段中的**每个** IP 测速（仅 IPv4），默认是每个 /24 随机一个。

> 6000 个目标会变成几十万个，非常慢。慎用。

---

## 常见组合速查

| 目标 | 命令 |
|---|---|
| 最快出结果 | `cfstx -dd` |
| 只看延迟 | `cfstx -dd -tl 200` |
| Worker 域名专用 | `cfstx -probe 你的域名 -dd` |
| 要速度 | `cfstx -sl 5 -dn 20` |
| 要特定机房 | `cfstx -httping -cfcolo HKG,NRT -dd` |
| 多来源合并 | `cfstx -bestcf -official -cmliu -dd` |
| 排查问题 | `cfstx -debug` |
| 看版本 | `cfstx -v` |

---

## 与图形界面的差异

| 功能 | 图形界面 | 命令行 |
|---|---|---|
| 优选 IP 测速 | ✅ | ✅ |
| 场景预设 | ✅ | ❌ 需自己组合参数 |
| 自定义测速域名 | ✅ | ✅ `-probe` |
| 结果表格 / 排序 / 推荐 | ✅ | ⚠️ 只输出文本 |
| 导出 CSV | ✅ | ✅ `-o` |
| 复制到剪贴板 | ✅ | ❌ |
| **ProxyIP 检测** | ✅ | ❌ |
| **Hosts 写入** | ✅ | ❌ |
| **应用内更新** | ✅ | ⚠️ `-v` 只检查不更新 |
| 系统代理提醒 | ✅ | ❌ |

---

## 输出文件

默认写入 `result.csv`，同目录下。

用 `-o ""` 关闭：

```bat
cfstx -o ""
```

CSV 字段与图形界面导出一致。

---

## 数据文件查找顺序

程序按以下顺序查找 `ip.txt`：

1. **当前工作目录**
2. **exe 所在目录**

所以从任意目录用绝对路径调用也能正常读取：

```bat
D:\tools\cfstx\cfstx.exe -dd
```

---

## `-v` 的行为

打印版本号，并检查是否有新版本：

```text
CFSTX v1.2.11
最新版本：v1.2.11（已是最新）
```

如果发现新版本，会提示下载地址。**不会自动更新**（那是图形界面的功能）。

---

## `-debug` 输出什么

开启后，在一些非预期情况下会输出更多日志，主要用于排查「下载速度为 0」：

| 报错 | 含义 |
|---|---|
| `connection reset by peer` | 链接被重置，测速地址或 IP 被阻断 |
| `HTTP 状态码: 403` | 测速地址拒绝访问（多半是 bytes 超限） |
| `HTTP 状态码: 429` | 触发配额限制 |
| `context deadline exceeded` | 请求超时，可能是 IP/网络问题，或单 IP 时长太短 |
| `tls: handshake failure` | 测速地址与测速 IP 不匹配 |
| `certificate is valid for XXX, not YYY` | 证书不包含该域名 |

---

## PowerShell 注意事项

PowerShell 下需要加 `.\`：

```powershell
.\cfstx.exe -dd
```

或者用完整路径：

```powershell
& "C:\path\to\cfstx.exe" -dd
```

---

## 相关

- [优选IP 详解](Speed-Test) —— 每个参数的含义和图形界面对应位置
- [常见问题](FAQ) —— 命令行的排查
