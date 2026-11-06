# CFSTX

> **本项目基于 [XIU2/CloudflareSpeedTest](https://github.com/XIU2/CloudflareSpeedTest) 二次开发，仅面向 Windows 平台。**
>
> 原作者：[**XIU2**](https://github.com/XIU2)　·　原项目地址：[https://github.com/XIU2/CloudflareSpeedTest](https://github.com/XIU2/CloudflareSpeedTest)
>
> 本仓库：[https://gitee.com/liliaic/cfstx](https://gitee.com/liliaic/cfstx)　·　许可证：[GPL-3.0](LICENSE)

---

## 简介

CFSTX 是一个 **Cloudflare CDN 优选 IP 测速工具**。它会批量测试 Cloudflare CDN 的 IP 延迟与下载速度，帮你从海量 IP 中筛出当前网络环境下最快的那几个。

国外很多网站都在使用 Cloudflare CDN，但分配给中国内地访客的 IP 往往延迟高、丢包多、速度慢。虽然 Cloudflare 公开了全部 [IP 段](https://www.cloudflare.com/zh-cn/ips/)，但手工从里面挑出适合自己的 IP 并不现实——这就是本工具要解决的问题。

本项目是 [XIU2/CloudflareSpeedTest](https://github.com/XIU2/CloudflareSpeedTest) 的衍生版本，**在原作者代码基础上二次开发**，主要差异：

- 项目更名为 **CFSTX**，模块路径与发布渠道迁移至 Gitee
- **移除 Linux / macOS 支持**，专注 Windows（x64 / x86）
- 删除跨平台 shell 脚本，仅保留 Windows 批处理脚本
- 版本更新检查指向本项目仓库

> [!IMPORTANT]
> Cloudflare CDN 已**明文禁止代理**方式使用，通过代理套 CDN 的请自行承担风险，请勿过度依赖。

---

## 特性

- **IPv4 + IPv6** 混合测速
- **TCPing / HTTPing** 两种延迟测速模式
- 按 **延迟、丢包率、下载速度** 设置过滤条件
- HTTPing 模式下可按 **地区码**（IATA / 国家码 / 城市码）筛选节点
- 结果导出 `result.csv`，方便二次处理
- 配套 Windows 脚本：自动更新 Hosts、3Proxy 配置、DDNS 解析

---

## 下载

前往 [**Gitee Releases**](https://gitee.com/liliaic/cfstx/releases) 选择适合你的版本：

| 版本 | 文件 | 说明 |
|---|---|---|
| **安装版（推荐）** | `CFSTX-Setup-x.x.x.exe` | 标准安装向导，可选安装路径，自动创建开始菜单和桌面快捷方式，支持卸载 |
| 免安装版 | `CFSTX-x.x.x-portable.zip` | 解压即用，不写注册表，适合放到 U 盘或临时使用 |

> [!NOTE]
> 由于暂未购买代码签名证书，首次运行安装包时 Windows SmartScreen 可能提示「**无法验证发布者**」。
> 这是未签名程序的正常提示，点击「**更多信息**」→「**仍要运行**」即可继续。

> 如果平均延迟异常低（如 0.xx），说明测速时走了代理，请先关闭代理软件再测速。

---

## 快速开始

最简单的方式：解压后直接双击 `cfstx.exe`，等待测速完成，结果会打印在窗口并保存到同目录的 `result.csv`。

带参数运行需要在 CMD 中执行，例如：

```bat
cfstx -tl 200 -dn 20
```

> PowerShell 下请把命令中的 `cfstx` 改为 `.\cfstx`。

### 在 CMD 中带参数运行

很多人直接以**绝对路径**运行会发现报错，因为默认参数 `-f ip.txt` 是相对路径。推荐先进入程序目录再运行：

**方式一**：打开程序目录 → 空白处按住 <kbd>Shift</kbd> + 鼠标右键 → 选择「在此处打开命令窗口」→ 输入命令。

**方式二**：打开程序目录 → 在文件夹地址栏输入 `cmd` 回车 → 输入命令。

**方式三**：随便打开 CMD，用 `cd /d "D:\Program Files\cfstx"` 进入目录。

### 用快捷方式带参数运行

右键 `cfstx.exe` →「创建快捷方式」→ 右键快捷方式 →「属性」→ 修改「目标」：

```bat
D:\ABC\cfstx\cfstx.exe -tl 200 -dn 20 -o " "

# 路径含空格时，把启动参数放在引号外面
"D:\Program Files\cfstx\cfstx.exe" -tl 200 -dn 20 -o " "
```

> 快捷方式的「起始位置」不能留空，否则会因绝对路径找不到 `ip.txt`。

---

## 参数说明

```text
C:\>cfstx -h

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
        下载测速时间；单个 IP 下载测速最长时间，不能太短；(默认 10 秒)
    -tp 443
        指定测速端口；延迟测速/下载测速时使用的端口；(默认 443 端口)
    -url https://cf.xiu2.xyz/url
        指定测速地址；延迟测速(HTTPing)/下载测速时使用的地址，默认地址不保证可用性，建议自建；
        当下载测速时，软件会从 HTTP 响应头中获取该 IP 当前地区码并显示出来。

    -httping
        切换测速模式；延迟测速模式改为 HTTP 协议，所用测试地址为 [-url] 参数；(默认 TCPing)
        当使用 HTTP 测速模式时，软件会从 HTTP 响应头中获取该 IP 当前地区码并显示出来。
        注意：HTTPing 本质上也算一种网络扫描行为，如果你在服务器上面运行，需要降低并发(-n)，
        否则可能会被一些严格的商家暂停服务。
    -httping-code 200
        有效状态代码；HTTPing 延迟测速时网页返回的有效 HTTP 状态码，仅限一个；(默认 200 301 302)
    -cfcolo HKG,KHH,NRT,LAX,SEA,SJC,FRA,MAD
        匹配指定地区；IATA 机场地区码或国家/城市码，英文逗号分隔，大小写均可，仅 HTTPing 模式可用；
        支持 Cloudflare、AWS CloudFront、Fastly、Gcore、CDN77、Bunny 等 CDN
        其中 Cloudflare、AWS CloudFront、Fastly 使用的是 IATA 三字机场地区码，如：HKG,LAX
        其中 CDN77、Bunny 使用的是 二字国家/区域码，如：US,CN
        其中 Gcore 使用的是 二字城市码，如：FR,AM

    -tl 200
        平均延迟上限；只输出低于指定平均延迟的 IP，各上下限条件可搭配使用；(默认 9999 ms)
    -tll 40
        平均延迟下限；只输出高于指定平均延迟的 IP；(默认 0 ms)
    -tlr 0.2
        丢包几率上限；只输出低于/等于指定丢包率的 IP，范围 0.00~1.00，0 过滤掉任何丢包的 IP；(默认 1.00)
    -sl 5
        下载速度下限；只输出高于指定下载速度的 IP，凑够指定数量 [-dn] 才会停止测速；(默认 0.00 MB/s)

    -p 10
        显示结果数量；测速后直接显示指定数量的结果，为 0 时不显示结果直接退出；(默认 10 个)
    -f ip.txt
        IP段数据文件；如路径含有空格请加上引号；支持其他 CDN IP段；(默认 ip.txt)
    -ip 1.1.1.1,2.2.2.2/24,2606:4700::/32
        指定IP段数据；直接通过参数指定要测速的 IP 段数据，英文逗号分隔；(默认 空)
    -o result.csv
        写入结果文件；如路径含有空格请加上引号；值为空时不写入文件 [-o ""]；(默认 result.csv)
        注意：在一些环境下使用 -o "" 可能会被忽略掉这个空参数导致报错，可加个空格 -o " " 解决

    -dd
        禁用下载测速；禁用后测速结果会按延迟排序 (默认按下载速度排序)；(默认 启用)
    -allip
        测速全部的IP；对 IP 段中的每个 IP (仅支持 IPv4) 进行测速；(默认 每个 /24 段随机测速一个 IP)

    -debug
        调试输出模式；会在一些非预期情况下输出更多日志以便判断原因；(默认 关闭)
    -v
        打印程序版本 + 检查版本更新
    -h
        打印帮助说明
```

> 所有参数都有默认值，按需使用即可，参数顺序无关。

---

## 使用示例

### IPv4 / IPv6

```bat
REM 测速自带的 IPv4 地址（-f 默认值就是 ip.txt，可省略）
cfstx -f ip.txt

REM 测速自带的 IPv6 地址
cfstx -f ipv6.txt

REM 直接通过参数指定
cfstx -ip 1.1.1.1,2606:4700::/32
```

> 本项目支持 IPv4 + IPv6 混合测速，一个文件内可以同时包含两种地址。

### HTTPing

```bat
REM 切换到 HTTP 协议延迟测速
cfstx -httping

REM 指定有效状态码
cfstx -httping -httping-code 200

REM 指定 HTTPing 测试地址
cfstx -httping -url https://cf.xiu2.xyz/url

REM 测试 80 端口
cfstx -httping -tp 80 -url http://cdn.cloudflare.steamstatic.com/steam/apps/5952/movie_max.webm
```

TCP 协议耗时更短、资源更少，超时 1 秒，是默认模式；HTTP 协议适用于验证某域名指向某 IP 时是否可访问，超时 2 秒。同一 IP 的延迟一般为 **ICMP < TCP < HTTP**。

### 匹配指定地区

```bat
cfstx -httping -cfcolo HKG,KHH,NRT,LAX,SEA,SJC,FRA,MAD
```

只有 HTTPing 模式可用，因为地区码来自 HTTP 响应头。可参考：

- IATA 三字机场码：<https://www.cloudflarestatus.com/>
- 二字国家码：[ISO 3166-1](https://zh.wikipedia.org/wiki/ISO_3166-1%E4%BA%8C%E4%BD%8D%E5%AD%97%E6%AF%8D%E4%BB%A3%E7%A0%81)

### 文件相对 / 绝对路径

```bat
REM 输出到文件，不显示结果（-p 0）
cfstx -f 1.txt -p 0 -dd

REM 显示结果，不输出文件
cfstx -f 2.txt -o "" -p 10 -dd

REM 相对路径
cfstx -f abc\3.txt -o abc\result.txt -dd

REM 绝对路径
cfstx -f C:\abc\4.txt -o C:\abc\result.csv -dd
```

> 以绝对路径运行程序时，`-f` / `-o` 也必须是绝对路径，否则会报错找不到文件。

### 测速其他端口

```bat
REM 测速 80 端口
cfstx -tp 80 -url http://speed.cloudflare.com/__down?bytes=99999999
```

非 80/443 端口需要确认你的测速地址支持通过该端口访问。

### 自定义测速地址

```bat
REM 地址要求：可直接下载、文件大于 200MB、使用 Cloudflare CDN
cfstx -url https://cf.xiu2.xyz/url
```

### 自定义过滤条件

```bat
REM 仅限制平均延迟上限（低于 200ms）
cfstx -tl 200

REM 限制延迟区间（60~200ms）
cfstx -tll 60 -tl 200

REM 只看不丢包的 IP
cfstx -tlr 0

REM 要求下载速度高于 5 MB/s，凑够 10 个
cfstx -sl 5 -dn 10

REM 延迟 + 速度组合
cfstx -tl 200 -sl 5.6 -dn 10
```

**几种条件组合的输出预期：**

| 条件 | 有满足项 | 无满足项 |
|---|---|---|
| 未指定任何延迟/速度条件 | 输出全部结果 | — |
| 指定了延迟条件（`-tl` / `-tll`） | 只输出满足条件的 IP | 输出空 |
| 指定了下载速度条件（`-sl`） | 只输出满足条件的 IP | 输出空 |
| 指定了 `-sl` 且开启 `-debug` | 只输出满足条件的 IP | 输出**全部**结果 |

> 只要指定了 `-sl` 且没凑够 `-dn` 数量，程序就会一直测下去。建议同时指定 `-tl`，避免耗时过长。

### 单独测速一个或多个 IP

```bat
REM 方式一：直接指定
cfstx -ip 1.1.1.1,2.2.2.2/24,2606:4700::/32

REM 方式二：写入 1.txt 后指定文件
cfstx -f 1.txt
```

`/24` 表示对该段随机取一个 IP 测速；要测整段请加 `-allip`（仅 IPv4）。

---

## 结果说明

测速完毕后默认显示最快的 10 个 IP：

```text
IP 地址           已发送  已接收  丢包率  平均延迟  下载速度(MB/s)  地区码
104.27.200.69     4      4       0.00   146.23    28.64          LAX
172.67.60.78      4      4       0.00   139.82    15.02          SEA
104.25.140.153    4      4       0.00   146.49    14.90          SJC
...
```

第一行就是**下载速度最快、平均延迟最低**的最优 IP。完整结果保存在 `result.csv`：

```csv
IP 地址,已发送,已接收,丢包率,平均延迟,下载速度(MB/s),地区码
104.27.200.69,4,4,0.00,146.23,28.64,LAX
```

> `result.csv` 用微软 Excel 打开可能中文乱码，用记事本或其他表格软件正常。

### 进度条数字怎么看

以 `-tll 40 -tl 150 -sl 1 -dn 5` 为例：

```text
开始延迟测速（模式：TCP, 端口：443, 范围：40 ~ 150 ms, 丢包：1.00)
321 / 321 [-------------------------] 可用: 30
开始下载测速（下限：1.00 MB/s, 数量：5, 队列：10）
3 / 5 [-------------------------↗---]
```

- `可用: 30` —— 321 个 IP 中，延迟测速未超时的有 30 个
- `队列：10` —— 再按延迟上下限和丢包条件过滤后，剩 10 个进入下载测速
- `3 / 5` —— 要求找到 5 个达到 1 MB/s 的 IP，实际只找到 3 个

所以「明明有 30 个可用，最后只剩 3 个」是条件过滤的正常结果，不是程序出错。

> 下载测速是单线程排队执行的，所以等待测速的 IP 数量叫「队列」。

---

## 常见问题

### 下载速度都是 0.00？

先加上 `-debug` 参数重新跑一遍，程序会输出具体报错：

| 报错 | 含义 |
|---|---|
| `connection reset by peer` | 链接被重置，测速地址或 IP 被阻断 |
| `HTTP 状态码: 403` | 测速地址拒绝访问 |
| `context deadline exceeded` | 请求超时，可能是 IP/网络问题，或 `-dt` 太短 |
| `tls: handshake failure` | 测速地址与测速 IP 不匹配（CDN 不一致） |
| `certificate is valid for XXX, not YYY` | 证书不包含该域名，IP 不可用于此地址 |
| `certificate has expired` | 证书过期或尚未生效 |
| `certificate signed by unknown authority` | 系统证书配置有问题 |

进一步排查：

1. **换测速地址**：参考 [#490](https://github.com/XIU2/CloudflareSpeedTest/discussions/490) 里的其他地址；想要稳定建议自建。
2. **换测速 IP**：部分 IP 能通 TCP 但无法建立 HTTP 连接（如回源 IP），多试几个。
3. **换网络**：用手机 USB 网络共享替代宽带，对比结果。

### 测速结果每次都不一样？

正常。每次都是在每个 IP 段中随机抽取 IP，结果不可能完全相同。

### 开机后第一次测速延迟偏高？

常见现象，后续测速会恢复正常。建议正式测速前先随便跑一次热身。

### 为什么延迟低的 IP 速度反而慢？

延迟和丢包最优的 IP 不一定速度最快，传输速度可能被 QOS 等策略限制。可以用 `-tll` 排除掉低延迟但速度差的 IP。

### 测速时走了代理怎么办？

如果平均延迟异常低（如 0.xx），请关闭代理软件后再测速。如果路由器上有代理，也要关闭或排除。

---

## 配套脚本

`script/` 目录提供 Windows 批处理脚本，用于把测速结果自动应用到系统配置：

| 脚本 | 用途 |
|---|---|
| `cfstx_hosts.bat` | 自动更新 Hosts 中的 Cloudflare CDN IP |
| `cfstx_3proxy.bat` | 更新 3Proxy 配置，一次性加速所有 Cloudflare 站点 |
| `cfstx_ddns.bat` | 通过 Cloudflare API 自动更新域名解析 |

详见 [script/README.md](script/README.md)。

---

## 手动编译

需要 Go 1.18 及以上版本。版本号通过 `-ldflags` 注入：

```bat
go build -ldflags "-s -w -X main.version=v1.0.0"
```

编译 32 位版本：

```bat
SET GOARCH=386
go build -ldflags "-s -w -X main.version=v1.0.0"
```

批量编译并指定输出路径：

```bat
SET version=v1.0.0
SET GOARCH=amd64
go build -o Releases\cfstx_windows_amd64\cfstx.exe -ldflags "-s -w -X main.version=%version%"
```

> 本项目仅面向 Windows。如需在非 Windows 环境交叉编译，请设置 `GOOS=windows`。

---

## 致谢

**CFSTX 的核心代码、算法与文档均来自以下开源项目，谨向原作者致以诚挚感谢：**

1. **[XIU2/CloudflareSpeedTest](https://github.com/XIU2/CloudflareSpeedTest)** —— 本项目直接基于它二次开发。原作者 [**XIU2**](https://github.com/XIU2) 从零实现了 Cloudflare IP 优选测速的完整方案，包括 TCPing/HTTPing 双模式、地区码识别、下载测速与丰富的过滤条件。本项目的 README 参数文档与故障排查内容同样源自该项目。
2. **[Spedoske/CloudflareScanner](https://github.com/Spedoske/CloudflareScanner)** —— CloudflareSpeedTest 的前身项目，提供了早期的基础实现。

如果你觉得这个工具好用，**请优先给[原作者的项目](https://github.com/XIU2/CloudflareSpeedTest)点一个 ⭐**，也欢迎关注作者的其他[开源作品](https://github.com/XIU2)。

---

## License

[GNU General Public License v3.0](LICENSE)

本项目作为 GPL-3.0 衍生作品发布，保留原项目的许可证与版权声明。任何基于本项目的分发或修改，均须遵守 GPL-3.0 的相应条款。
