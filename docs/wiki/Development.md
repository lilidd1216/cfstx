# 开发指南

面向想自己编译、改代码、提 PR 的人。

---

## 环境要求

| 组件 | 版本 | 用途 |
|---|---|---|
| **Go** | 1.26+ | 后端 / 桌面主程序 |
| **Node.js** | 18+ | 前端构建 |
| **Inno Setup 6** | 可选 | 编译安装包 |

Windows 10 / 11 x64。

---

## 项目结构

```text
cfstx/
├── main.go                  程序入口：无参数进 GUI，带参数进 CLI
├── app.go                   App 结构体：配置、状态、日志
├── api.go                   本地 HTTP API（REST + SSE）
├── events.go                事件中心（SSE 广播）
├── gui.go                   WebView2 窗口
├── proxyip_app.go           ProxyIP 检测的 App 层
├── hosts_windows.go         Hosts 读写与校验
│
├── task/                    测速核心（从上游继承 + 大量改造）
│   ├── ip.go                IP 加载、去重、端口优先级
│   ├── tcping.go            TCPing 延迟测速
│   ├── httping.go           HTTPing 延迟测速
│   ├── download.go          下载测速
│   ├── probe.go             自定义域名探测
│   ├── bestcf.go            在线来源抓取
│   ├── proxyip.go           ProxyIP 检测（五步判定）
│   ├── proxyip_sources.go   ProxyIP 数据源
│   └── ...
│
├── cli/                     命令行模式
├── version/                 版本号、更新检查
├── utils/                   工具函数
│
├── frontend/                Vue 3 + Vuetify 3
│   ├── src/App.vue          主界面、页面切换、SSE 事件
│   ├── src/components/
│   │   ├── OptimizePanel.vue    优化工具
│   │   └── ProxyIPPanel.vue     ProxyIP 检测
│   └── dist/                构建产物（go:embed）
│
├── assets/                  图标等静态资源
├── installer/               Inno Setup 脚本
├── script/                  配套批处理脚本
├── docs/
│   ├── images/              README 截图
│   └── wiki/                Wiki 源文件（本文档）
│
├── build.ps1                构建前端 + exe
├── release.ps1              完整发布产物
└── installer.iss            安装包定义
```

---

## 构建

### 最简单的方式

```powershell
.\build.ps1
```

它会：

1. 构建前端（`npm run build`）
2. 编译 exe 到 `build\bin\cfstx.exe`
3. 修正 exe 的完整性级别

### 手动构建

```bat
REM 1. 前端
cd frontend
npm install
npm run build
cd ..

REM 2. 后端（前端产物通过 go:embed 打进 exe）
go build -ldflags "-H windowsgui" -o build\bin\cfstx.exe .

REM 3. 可选：安装包
ISCC.exe installer.iss
```

### `-H windowsgui` 是什么

生成 **GUI 子系统**程序，双击时不会弹出黑色控制台窗口。

命令行模式下由 `console_windows.go` 中的 `AttachConsole` 重新附加到父控制台。

### 图标

图标资源 `assets/cfstx.ico`，通过 `rsrc_windows_amd64.syso` 自动嵌入。

重新生成见 `build/make-icon.ps1`。

---

## 测试

```powershell
# 全部单元测试
go test ./...

# 带竞态检测
go test -race ./...

# 端到端（需要外网）
$env:CFSTX_E2E=1; go test -run TestProxyIPAPIFlow -v .

# 起本地服务，用浏览器手工验界面
$env:CFSTX_SERVE=1; go test -run TestServeForBrowser -timeout 0 -v .
# 地址写在 build/tmp/serve_url.txt
```

### 测试文件分布

| 文件 | 覆盖 |
|---|---|
| `task/probe_test.go` | 域名规范化 |
| `task/proxyip_test.go` | 候选解析、CF 网段、trace 解析、排序、格式化 |
| `hosts_windows_test.go` | Hosts 读写、端口校验、多域名 |
| `proxyip_e2e_test.go` | 端到端 HTTP 流程 |

### 关于 `-race`

**每次改并发代码都应该跑。**

v1.2.11 就是靠它发现了一个从 v1.2.0 就存在的竞态：`ProbeProxyIPs` 的回调被多条 goroutine 并发调用，计数器没加锁。

---

## 架构

### 三层结构

```text
┌─────────────────────────────────────────┐
│  Vue 3 前端（WebView2 里的网页）          │
│  · 界面渲染、用户交互                     │
│  · 通过 fetch + SSE 与后端通信            │
└──────────────┬──────────────────────────┘
               │ HTTP / SSE（127.0.0.1:随机端口）
┌──────────────▼──────────────────────────┐
│  Go 后端（App + API + EventHub）         │
│  · 业务逻辑、状态管理                     │
│  · task/ 包做实际测速                     │
└──────────────┬──────────────────────────┘
               │
┌──────────────▼──────────────────────────┐
│  网络（Cloudflare / 在线数据源）          │
└─────────────────────────────────────────┘
```

### 为什么用本地 HTTP 而不是 Wails/Electron

| 方案 | 优点 | 缺点 |
|---|---|---|
| Wails | 生态成熟 | 依赖重，构建慢 |
| Electron | 前端自由 | 体积巨大（100MB+） |
| **本地 HTTP + WebView2** | 体积小（10MB）、构建快、前后端完全解耦 | 需要自己实现 IPC |

本项目选择第三种：Go 起一个 `127.0.0.1:0` 的 HTTP 服务（随机端口），WebView2 加载这个地址。

**好处**：

- 前后端可以独立开发调试（浏览器直接开 `http://127.0.0.1:端口`）
- 不需要为每个功能写 IPC 绑定
- SSE 天然支持服务端推送

### 通信方式

| 场景 | 方式 |
|---|---|
| 发命令（开始测速、停止） | `POST /api/...` |
| 查状态（刷新后恢复） | `GET /api/...` |
| 实时进度 | `GET /api/events`（SSE） |

**SSE 事件命名**：`<模块>:<动作>`，如 `proxyip:progress`、`speed:finished`。

### 关键文件职责

| 文件 | 职责 |
|---|---|
| `main.go` | 判断有无参数 → CLI 或 GUI |
| `app.go` | 全局状态（配置、结果、日志），各模块共享 |
| `api.go` | 所有 HTTP 路由 |
| `events.go` | SSE 订阅与广播（非阻塞） |
| `task/*.go` | 纯计算，不依赖 App |

---

## 代码约定

### 注释

**写「为什么」，不写「是什么」。**

```go
// 好的注释：解释反直觉的地方
// Windows 的 DNS 查询不支持 context 取消，改用定时器兜底，
// 防止个别黑洞域名拖死整批检测。
func lookupIPsWithTimeout(...)

// 差的注释：重复代码
// 设置超时
timeout = 3 * time.Second
```

### 错误信息

**面向用户，说人话。**

```go
// 好
return fmt.Errorf("%s 端口不能用：hosts 只做域名解析，浏览器访问 https 固定走 443，写进去会连不上", port)

// 差
return fmt.Errorf("invalid port")
```

### 前端

- Vue 3 `<script setup>` + Composition API
- Vuetify 3 组件
- 深色主题，配色见各组件 `<style scoped>`
- 状态由 SSE 驱动，组件只负责展示

### 并发

- 用 `sync.Mutex` 保护共享状态
- 回调可能被并发调用 —— **假设它一定会并发**
- 每次改并发代码跑 `go test -race`

---

## 发布流程

```powershell
# 1. 更新版本号
#    version/version.go  → var Current = "v1.2.12"
#    version.txt         → v1.2.12

# 2. 更新 README / CHANGELOG

# 3. 提交
git add -A
git commit -m "..."

# 4. 构建产物
.\release.ps1

# 5. 打 tag 并推送
git tag -a v1.2.12 -m "v1.2.12"
git push origin master
git push origin v1.2.12

# 6. 在 Gitee 创建 Release，上传：
#    dist\CFSTX-Setup-1.2.12.exe
#    dist\CFSTX-1.2.12-portable.zip
```

`release.ps1` 会做：

1. 同步 `version.txt` 和 `installer.iss`
2. 构建前端
3. 编译 exe
4. **校验 exe 时间戳晚于所有源码**（防止打包旧代码）
5. 编译安装包
6. 打包免安装 zip

> [!WARNING]
> 第 4 步的校验很关键。v1.0.0 就因为提交源码后没重新编译，导致安装包里缺少更新接口。

---

## 常见开发问题

### 前端改了没生效

`go:embed` 在编译时打包前端产物。改完前端要**重新 `npm run build` 再 `go build`**。

开发时可以用 `CFSTX_SERVE=1` 模式，前端走磁盘，改完刷新即可。

### esbuild 报权限错误

在低完整性沙箱目录下常见。把 `TEMP` / `TMP` 指向项目内目录：

```powershell
$env:TEMP = "项目路径\build\tmp"
$env:TMP  = "项目路径\build\tmp"
```

`build.ps1` 和 `release.ps1` 已处理。

### exe 启动报 0x8000FFFF

构建产物继承了低完整性级别，WebView2 无法创建子进程。

```powershell
icacls "build\bin\cfstx.exe" /setintegritylevel Medium
```

`build.ps1` 已处理。

### WebView2 加载空白

检查 `frontend/dist/` 是否有内容。`go:embed all:frontend/dist` 需要至少一个文件，所以仓库里保留了 `frontend/dist/.gitkeep`。

---

## 贡献

1. Fork 仓库
2. 建分支：`git checkout -b fix/xxx`
3. 改代码 + 补测试
4. `go test -race ./...` 通过
5. 提 PR，说明改了什么、为什么

**不接受**：

- 破坏 Windows 专注性的改动（如恢复 Linux 支持）
- 未经测试的并发代码
- 面向用户但不说人话的错误信息

---

## 相关

- [原理与限制](How-It-Works) —— 理解业务逻辑
- [更新日志](Changelog) —— 改了什么
