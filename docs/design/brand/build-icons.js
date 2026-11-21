// 从 mark.svg (完整标志) 与 glyph.svg (小尺寸符号) 生成 CZL Mail 全部图标资源 (规格见 czl-skill product-logo.md §7)。
// 两个 SVG 拷贝自 czl-skill references/product-logo/{marks,glyphs}/czlmail.svg, 是出图的单一来源; 改造型先改 czl-skill 再拷回来。
// 依赖 @resvg/resvg-js@2.6.2, 仓库不留 node_modules: 在任意临时目录 `npm i --no-save @resvg/resvg-js@2.6.2`,
// 再用 NODE_PATH 指过去运行; 之后跑 build-ico.py 打包 ico 并给满幅图标去 alpha:
//   NODE_PATH=<临时目录>/node_modules node docs/design/brand/build-icons.js
//   python docs/design/brand/build-ico.py
// iOS 图标在私有仓库 czlmail-mobile (与本仓库并排检出), 存在时一并生成。
const fs = require('fs')
const os = require('os')
const path = require('path')
const { Resvg } = require('@resvg/resvg-js')

const ROOT = path.resolve(__dirname, '../../..')
const BRAND = __dirname
const DESK = path.join(ROOT, 'desktop')
const NOTE = path.join(ROOT, 'web-phone-note')
const RES = path.join(ROOT, 'android/native/app/src/main/res')
const IOS = path.resolve(ROOT, '../czlmail-mobile/ios/App/App/Assets.xcassets/AppIcon.appiconset')
const TMP = path.join(os.tmpdir(), 'czlmail-brand') // build-ico.py 读取的中间文件
fs.mkdirSync(TMP, { recursive: true })

// 读出单 path 的 SVG: 视窗 (x, y, w, h) 与路径
const load = (file) => {
  const s = fs.readFileSync(path.join(BRAND, file), 'utf8')
  const [x, y, w, h] = s.match(/viewBox="([^"]+)"/)[1].trim().split(/[\s,]+/).map(Number)
  return { x, y, w, h, d: s.match(/<path[^>]*\sd="([^"]+)"/)[1] }
}
const mark = load('mark.svg')
const glyph = load('glyph.svg')
const C1 = '#2EA7E0'
const C2 = '#246CC8'
const SPLASH_BG = '#F4F5F8' // 与界面亮色底一致, 启动屏切到界面时不闪
const f = (n) => +n.toFixed(3)

const gradDef = `<defs><linearGradient id="g" gradientUnits="userSpaceOnUse" x1="${mark.x}" y1="0" x2="${mark.x + mark.w}" y2="0"><stop offset="0" stop-color="${C1}"/><stop offset="1" stop-color="${C2}"/></linearGradient></defs>`
const svg = (w, h, body) => `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${w} ${h}" width="${w}" height="${h}">${body}</svg>\n`

// 把图形放进 w×h 画布: 长边占 long 像素, 水平垂直居中
const place = (g, w, h, long, fill) => {
  const s = long / Math.max(g.w, g.h)
  const tx = (w - g.w * s) / 2 - g.x * s
  const ty = (h - g.h * s) / 2 - g.y * s
  return `<path fill="${fill}" transform="translate(${f(tx)} ${f(ty)}) scale(${f(s)})" d="${g.d}"/>`
}

// 透明底完整标志 (桌面图标、安装包、PWA any): 长边占 92%; white=true 为纯白版
const markIcon = (size, white = false) =>
  svg(size, size, (white ? '' : gradDef) + place(mark, size, size, size * 0.92, white ? '#fff' : 'url(#g)'))
// 白色满幅底 (iOS / Android 启动器 / apple-touch / maskable): 标志长边占 ratio
const solidIcon = (size, ratio = 0.7, shape = 'full') =>
  svg(
    size,
    size,
    gradDef +
      (shape === 'circle' ? `<circle cx="${size / 2}" cy="${size / 2}" r="${size / 2}" fill="#fff"/>` : `<rect width="${size}" height="${size}" fill="#fff"/>`) +
      place(mark, size, size, size * ratio, 'url(#g)'),
  )
// 小尺寸符号: 透明底实色, 撑满画布 (每边留 pad 像素抗锯齿余量)
const glyphIcon = (size, color = C1, pad = 0.5) => svg(size, size, place(glyph, size, size, size - 2 * pad, color))

const png = (s) => new Resvg(s).render().asPng()
const write = (file, data) => {
  fs.mkdirSync(path.dirname(file), { recursive: true })
  fs.writeFileSync(file, data)
  console.log('wrote', path.relative(ROOT, file))
}
const tmp = (name, s) => write(path.join(TMP, name), png(s))

// 桌面端: 透明底完整标志, Wails 由 appicon.png 生成 macOS icns
write(path.join(DESK, 'build/appicon.png'), png(markIcon(1024)))
// Windows icon.ico (安装包与程序图标): 16 / 20 小尺寸符号, 24 起透明底完整标志
for (const s of [16, 20]) tmp(`app-${s}.png`, glyphIcon(s))
for (const s of [24, 32, 48, 64, 128, 256]) tmp(`app-${s}.png`, markIcon(s))
// 托盘: Windows 多帧 ico 覆盖 100%-200% 缩放, 16 / 20 符号、24 / 32 完整标志; 32px 另存一份作未读角标的底图
for (const s of [16, 20]) tmp(`tray-${s}.png`, glyphIcon(s))
for (const s of [24, 32]) tmp(`tray-${s}.png`, markIcon(s))
write(path.join(DESK, 'build/tray/tray-32.png'), png(markIcon(32)))
// macOS 菜单栏 template: 纯黑 + alpha, 系统按主题着色
write(path.join(DESK, 'build/tray/menubar.png'), png(glyphIcon(18, '#000')))
write(path.join(DESK, 'build/tray/menubar@2x.png'), png(glyphIcon(36, '#000', 1)))

// 桌面端界面: 左侧导航 (32px) 与「关于」(48px) 都用完整标志, 亮色渐变 / 暗色白色
write(path.join(DESK, 'frontend/public/logo.svg'), markIcon(256))
write(path.join(DESK, 'frontend/public/logo-white.svg'), markIcon(256, true))
// favicon: 16 符号, 32 / 48 完整标志
tmp('fav-16.png', glyphIcon(16))
for (const s of [32, 48]) tmp(`fav-${s}.png`, markIcon(s))

// 手机看笔记网页
write(path.join(NOTE, 'app/icon.svg'), glyphIcon(256, C1, 8))
write(path.join(NOTE, 'public/images/icon-192.png'), png(markIcon(192)))
write(path.join(NOTE, 'public/images/icon-512.png'), png(markIcon(512)))
write(path.join(NOTE, 'public/images/apple-touch-icon.png'), png(solidIcon(180)))
write(path.join(NOTE, 'public/images/icon-maskable-512.png'), png(solidIcon(512, 0.6)))

// Android: 旧版方形 / 圆形图标 (48dp, 白底), 自适应图标前景 (108dp, 标志落在中间 66dp 安全区; 背景色白见 values/), 启动屏
const DENS = { mdpi: 1, hdpi: 1.5, xhdpi: 2, xxhdpi: 3, xxxhdpi: 4 }
for (const [d, k] of Object.entries(DENS)) {
  write(path.join(RES, `mipmap-${d}/ic_launcher.png`), png(solidIcon(48 * k)))
  write(path.join(RES, `mipmap-${d}/ic_launcher_round.png`), png(solidIcon(48 * k, 0.62, 'circle')))
  write(path.join(RES, `mipmap-${d}/ic_launcher_foreground.png`), png(svg(108 * k, 108 * k, gradDef + place(mark, 108 * k, 108 * k, 60 * k, 'url(#g)'))))
}
const splash = (w, h) => svg(w, h, gradDef + `<rect width="${w}" height="${h}" fill="${SPLASH_BG}"/>` + place(mark, w, h, 0.3 * Math.min(w, h), 'url(#g)'))
for (const dir of fs.readdirSync(RES).filter((d) => d.startsWith('drawable'))) {
  const p = path.join(RES, dir, 'splash.png')
  if (!fs.existsSync(p)) continue
  const buf = fs.readFileSync(p) // 沿用原尺寸 (PNG IHDR 宽高)
  write(p, png(splash(buf.readUInt32BE(16), buf.readUInt32BE(20))))
}
// 通知小图标: 系统只取 alpha, 白色剪影; 24dp 画布四周留 1dp。内层组先把视窗原点移到 0, 外层再缩放居中
const ns = 22 / Math.max(glyph.w, glyph.h)
write(
  path.join(RES, 'drawable/ic_stat_mail.xml'),
  `<?xml version="1.0" encoding="utf-8"?>
<!-- 状态栏小图标: 系统只取 alpha, 用小尺寸符号的白色剪影。由 docs/design/brand/build-icons.js 生成, 不要手改。 -->
<vector xmlns:android="http://schemas.android.com/apk/res/android"
    android:width="24dp"
    android:height="24dp"
    android:viewportWidth="24"
    android:viewportHeight="24">
    <group
        android:translateX="${f((24 - glyph.w * ns) / 2)}"
        android:translateY="${f((24 - glyph.h * ns) / 2)}"
        android:scaleX="${f(ns)}"
        android:scaleY="${f(ns)}">
        <group
            android:translateX="${f(-glyph.x)}"
            android:translateY="${f(-glyph.y)}">
            <path
                android:fillColor="#FFFFFFFF"
                android:pathData="${glyph.d}" />
        </group>
    </group>
</vector>
`,
)

// iOS (私有仓库): 1024 白色满幅底, build-ico.py 去 alpha
if (fs.existsSync(IOS)) write(path.join(IOS, 'AppIcon-512@2x.png'), png(solidIcon(1024)))

// 验收预览用 (不进仓库)
for (const s of [16, 20]) tmp(`glyph-${s}.png`, glyphIcon(s))
for (const s of [24, 32, 48]) {
  tmp(`mark-${s}.png`, markIcon(s))
  tmp(`mark-white-${s}.png`, markIcon(s, true))
}
