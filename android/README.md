# CZL Mail Android

Capacitor 壳。界面是 `desktop/frontend` 的静态导出，内核是 `desktop` 的 Go 包（`-buildmode=c-shared` 编成 `libczlcore.so`，经 JNI 调用），与桌面端共用一份代码。

## 构建

依赖：Go（版本见 `desktop/go.mod`）、JDK 21、Android SDK（compileSdk 36）与 NDK、Node 22、pnpm。

```bash
pnpm --dir ../desktop/frontend install
cd ../desktop && wails generate module && cd ../android   # 生成前端调用的 Go 绑定 (wailsjs)
npm install
npm run build:web      # 界面
npm run build:core     # Go 内核 → native/app/src/main/jniLibs/{arm64-v8a,x86_64}/libczlcore.so
npx cap sync android   # 把 out/ 复制进工程
cd native && ./gradlew assembleDebug
```

改了 Go 代码只需重跑 `build:core` 与 gradle；改了界面重跑 `build:web` 与 `cap sync`。
调试用模拟器时只编 x86_64 更快：`bash scripts/build-core.sh x86_64`。

发版包由 CI（`.github/workflows/release.yml` 的 `android` 任务）随 `v*` tag 构建，签名密钥在仓库 secrets：
`CZL_ANDROID_KEYSTORE_BASE64`（.jks 的 base64）、`CZL_ANDROID_KEYSTORE_PASSWORD`、`CZL_ANDROID_KEY_ALIAS`、`CZL_ANDROID_KEY_PASSWORD`。
本地签名时把同名环境变量（`CZL_ANDROID_KEYSTORE` 为 .jks 路径）设好再 `./gradlew assembleRelease`。
版本号取 git tag（或 `VERSION` 环境变量），`versionCode = X*1000000 + Y*1000 + Z`。

## 结构

```
capacitor.config.json   appId net.czl.mail, webDir 指向 ../desktop/frontend/out, 原生工程在 native/
scripts/build-core.sh   Go 内核 → jniLibs
native/app/src/main/java/net/czl/mail/
  MainActivity          启动内核、注册插件、接管 /czl-* 与返回键、转交链接与通知点击
  core/Core             内核包装: 环境变量、启动、调用排队
  core/Native           JNI 声明 (实现在 desktop/mobile_jni_android.c)
  core/CzlCorePlugin    页面 ↔ 内核的 Capacitor 插件 (页面侧是 desktop/frontend/lib/native-bridge.ts)
  core/HostHandler      内核发来的宿主请求: 文件选择、打开文件/链接、通知、凭据、保存目录、安装更新
  core/SecretStore      凭据加密存储 (Android Keystore AES-GCM)
  core/LocalWebViewClient  同源 /czl-* 请求交给 Go 的本地路径处理器
  core/SyncService      后台同步前台服务; core/BootReceiver 开机与更新后拉起
  core/Notifications    通知渠道与通知
```

Go 侧的安卓专属代码：`desktop/host_android.go`（宿主能力、凭据、OAuth 回跳）、`desktop/mobile_jni_android.c`、
`desktop/internal/keychain/keychain_android.go`、`desktop/internal/sqlitedb/mattn_android.go`；与 iOS 共用的在 `desktop/mobile_core.go`、`host_mobile.go`。

## 平台差异与约束

- **调用链**：界面照常调 `wailsjs/go/main/App`，`native-bridge.ts` 转成插件 `call`，Java 在后台线程调 JNI → `internal/rpc` 按方法名反射调 `App`。
  Go 的事件经 JNI 回调到插件 `notifyListeners`。字符串一律以 UTF-8 `byte[]` 过 JNI：`NewStringUTF` 的 modified UTF-8 处理不了 emoji
- **环境变量**：`HOME`（配置、缓存库、日志落在 `files/.config/czlmail`）、`XDG_CACHE_HOME`/`TMPDIR`（缓存目录）、`TZ` 由 `Native.setenv` 交给 Go。
  不能用 `Os.setenv`：c-shared 的 Go 运行时取的是进程启动时的环境，加载之后再 setenv 它看不到
- **SQLite**：安卓用 cgo 的 `mattn/go-sqlite3`（`-tags sqlite_fts5`），桌面与 iOS 仍是纯 Go 的 modernc。modernc 在 x86_64 上调用 `lstat`/`open` 等旧式系统调用，
  被 Android 的 seccomp 以 SIGSYS 杀掉。模拟器也不能靠 ARM 转译跑 arm64 内核（转译器不认 Go 生成的部分指令，SIGILL），所以包里带 x86_64
- **本地路径**：页面源是 `https://localhost`，`/czl-*` 与页面同源，由 `LocalWebViewClient` 拦截，`localUrl()` 在安卓返回相对路径（iOS 才需要 `czl-local://`）
- **凭据**：Android Keystore 里生成不可导出的 AES 密钥，密文存私有 SharedPreferences；没有明文回落。程序关闭备份（密钥不随备份迁移，恢复后的密文解不开）
- **后台收信**：Stalwart 只有 EventSource 推送，没有能直达手机的通道。`SyncService`（`specialUse` 前台服务）让进程与长连接常驻，
  常驻通知走最低重要性的「后台同步」渠道，用户可单独关掉而不影响「新邮件与提醒」。系统省电策略严格的机型仍可能断开，需用户把程序设为不受电池优化限制
- **通知**：Android 13+ 启动时请求通知权限；点击通知带 ActionID 回到 `MainActivity` 交给内核（与桌面端同一套 `email:`/`event:`/`update:` 分派）。启动器角标由系统按通知数决定
- **OAuth**：授权页开在系统浏览器，回调仍走内核的回环端口；回环页 302 到 `czlmail://signin`（`auth.ReturnURL`）让浏览器把程序带回前台——后台进程自己无法启动界面
- **返回键**：先交给界面 `window.__czlBack()`（`lib/native-back.ts`：关弹窗、详情回列表、收侧栏，登记方是 `useEdgeSwipe` 的返回手势与侧栏抽屉），界面不处理时 `moveTaskToBack` 退到后台，不结束进程
- **文件**：选择文件复制进缓存目录再交给内核（Go 读不了 content://）；不支持选择文件夹（导入/导出笔记库、上传文件夹报 2270）。
  保存到「下载/CZL Mail」（Android 11+，不需要存储权限）；打开附件经 FileProvider 交给系统程序，没有程序能打开时给分享面板
- **在线更新**：与桌面端同一套 GitHub Releases 检查（`czlmail-android.apk`，SHA256SUMS 校验），下载后交给系统安装器；首次需要用户允许「安装未知应用」。签名密钥一旦更换，已安装的用户无法覆盖升级
- **链接**：清单登记 `czlmail://`（复制邮件链接、OAuth 回跳）与 `mailto:`
- 不提供：MCP、托盘、开机自启设置、默认应用设置、退出、「删除所有数据」（系统设置里清除数据即可）

## 未完成

- 真机验证（目前只在 x86_64 模拟器上跑过）；armeabi-v7a 未验证，不在发版包里
- 写信面板的键盘遮挡、分享到本程序（ACTION_SEND）、`.ics` 文件打开
