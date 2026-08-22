# Jarvis Android

这是现有 Jarvis Web 前端的 Android 客户端外壳。App 使用系统 WebView 连接已部署的前端和 Gateway，因此登录、Chat、Code、Stock、Providers、Sessions、Tags 与 Settings 共用现有服务端数据，不在手机中复制后端逻辑。

## 环境要求

- Android Studio（建议使用自带的 JDK 17）
- Android SDK 35
- Android 8.0（API 26）或更高版本的设备
- 已部署并可由手机访问的 Jarvis Web/Gateway 地址

## 构建和运行

1. 使用 Android Studio 打开仓库中的 `Android` 目录。
2. 等待 Gradle Sync 下载 Android Gradle Plugin 和 Kotlin 插件。
3. 选择 `app` 配置和设备，运行 Debug 版本。
4. 首次启动输入服务器地址，例如 `https://jarvis.example.com`。

命令行构建：

```bash
cd Android
./gradlew assembleDebug
```

APK 输出到 `Android/app/build/outputs/apk/debug/app-debug.apk`。

## 服务器要求

服务器地址必须同时提供编译后的 Web 前端和同源接口：

- Web 页面：`/`
- REST API：`/v1/...`
- WebSocket：`/ws/...`

生产 Release 包只接受 HTTPS，且不会忽略无效证书。开发 Debug 包允许 HTTP；Android 模拟器访问电脑本机服务时应使用 `http://10.0.2.2:端口`，真机需要使用电脑的局域网 IP，并确保防火墙和 Gateway 监听地址允许访问。

不要把仅监听 `127.0.0.1` 的 Gateway 地址填入真机。推荐在 Gateway 前放置 Nginx 或 Caddy，由它提供 Web 静态文件、TLS，并反向代理 `/v1` 与 `/ws`。

## App 行为

- 服务器地址保存在 Android 私有配置中；右上角设置按钮可切换服务器。
- WebView 的 Local Storage、Cookie 和登录会话会跨启动保留。
- 同源页面在 App 内打开，GitHub/OAuth、新闻等外部链接由系统浏览器打开。
- SSE、流式 Fetch 和 WebSocket 由 WebView 直接连接服务器。
- Android 返回键优先返回网页历史，网页无历史时退出当前 Activity。
- 工作区 Blob 下载会分块写入临时文件，再打开 Android 保存位置选择器。

## 文件上传限制

普通文件和 Markdown 导入使用 Android 系统文件选择器。Web 端的工作区上传控件使用 `webkitdirectory`，但 Android 文件选择器不能像桌面 Chrome 一样返回完整目录树和 `webkitRelativePath`。App 会允许多选文件，但这些文件在上传时只有文件名，没有原始目录层级；需要保留完整项目结构时，请使用页面中的 GitHub 导入功能。

当前 Web 工作区入口会在浏览器端重新打包所选文件并应用大小/敏感文件过滤规则。Android App 本身不会绕过服务端的上传限制。

## 发布签名

当前 `release` 构建未包含私有签名。发布前在本机创建 keystore，并通过不提交到 Git 的本地 Gradle 配置注入 `signingConfig`。不要把 keystore、alias 或密码提交到仓库。
