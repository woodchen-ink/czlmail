// 从 mark.json (完整标志) 与 glyph.json (小尺寸符号) 生成 CZL Mail 全部图标资源 (规格见 czl-skill product-logo.md §7)。
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

const mark = JSON.parse(fs.readFileSync(path.join(BRAND, 'mark.json'), 'utf8'))
const glyph = JSON.parse(fs.readFileSync(path.join(BRAND, 'glyph.json'), 'utf8'))
const C1 = '#2EA7E0'
const C2 = '#1D2088'
const RADIUS = 0.2237
const SPLASH_BG = '#F4F5F8' // 与界面亮色底一致, 启动屏切到界面时不闪
const f = (n) => +n.toFixed(3)

const gradDef = `<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="0"><stop offset="0" stop-color="${C1}"/><stop offset="1" stop-color="${C2}"/></linearGradient></defs>`
const svg = (w, h, body) => `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${w} ${h}" width="${w}" height="${h}">${body}</svg>\n`

// 标志放进 w×h 画布: 宽占 ratio, 水平垂直居中
const placeMark = (w, h, ratio, fill) => {
  const s = (w * ratio) / mark.w
  return `<path fill="${fill}" transform="translate(${f((w - mark.w * s) / 2)} ${f((h - mark.h * s) / 2)}) scale(${f(s)})" d="${mark.d}"/>`
}

// 透明底标志, 紧贴包围盒; gradient=false 为纯白
const markSvg = (gradient) =>
  svg(mark.w, mark.h, (gradient ? gradDef : '') + `<path fill="${gradient ? 'url(#g)' : '#fff'}" d="${mark.d}"/>`)

// 应用图标: 白底 + 横向渐变标志。shape: full = 满幅直角 (iOS / maskable / 自适应图标由系统裁形),
// rounded = 圆角底板 + 极浅灰描边 (不加投影的平台边缘不至于消失), circle = 圆形 (Android 旧版圆形图标)。
// inset = 底板四周留白占画布比例 (macOS 图标网格 1024 里 824 的底板)
const tileSvg = (size, ratio, shape, inset = 0) => {
  const m = size * inset
  const p = size - 2 * m
  const sw = Math.max(1, size / 256)
  let plate
  if (shape === 'full') plate = `<rect width="${size}" height="${size}" fill="#fff"/>`
  else if (shape === 'circle')
    plate = `<circle cx="${size / 2}" cy="${size / 2}" r="${f(size / 2 - sw / 2)}" fill="#fff" stroke="#E4E6EB" stroke-width="${f(sw)}"/>`
  else
    plate = `<rect x="${f(m + sw / 2)}" y="${f(m + sw / 2)}" width="${f(p - sw)}" height="${f(p - sw)}" rx="${f(p * RADIUS - sw / 2)}" fill="#fff" stroke="#E4E6EB" stroke-width="${f(sw)}"/>`
  const s = (p * ratio) / mark.w
  return svg(
    size,
    size,
    gradDef + plate + `<path fill="url(#g)" transform="translate(${f((size - mark.w * s) / 2)} ${f((size - mark.h * s) / 2)}) scale(${f(s)})" d="${mark.d}"/>`,
  )
}

// 小尺寸符号: 透明底实色, 撑满画布 (每边留 pad 像素抗锯齿余量); size 省略则紧贴包围盒
const glyphSvg = (color, size, pad = 0.5) => {
  if (!size) return svg(glyph.w, glyph.h, `<path fill="${color}" d="${glyph.d}"/>`)
  const s = (size - 2 * pad) / Math.max(glyph.w, glyph.h)
  return svg(
    size,
    size,
    `<path fill="${color}" transform="translate(${f((size - glyph.w * s) / 2)} ${f((size - glyph.h * s) / 2)}) scale(${f(s)})" d="${glyph.d}"/>`,
  )
}

const png = (s) => new Resvg(s).render().asPng()
const write = (file, data) => {
  fs.mkdirSync(path.dirname(file), { recursive: true })
  fs.writeFileSync(file, data)
  console.log('wrote', path.relative(ROOT, file))
}
const tmp = (name, s) => write(path.join(TMP, name), png(s))

// 设计源与样板图
write(path.join(BRAND, 'mark.svg'), markSvg(true))
write(path.join(BRAND, 'mark-white.svg'), markSvg(false))
write(path.join(BRAND, 'glyph.svg'), glyphSvg(C1))
write(path.join(BRAND, 'app-icon.svg'), tileSvg(1024, 0.78, 'full'))
write(path.join(BRAND, 'app-icon-512.png'), png(tileSvg(512, 0.78, 'rounded')))
write(path.join(BRAND, 'glyph-256.png'), png(glyphSvg(C1, 256, 8)))

// 桌面端: macOS 按 Apple 图标网格 (1024 里 824 的底板), Wails 由它生成 icns
write(path.join(DESK, 'build/appicon.png'), png(tileSvg(1024, 0.78, 'rounded', 100 / 1024)))
// Windows icon.ico (安装包与程序图标): 16 / 24 / 32 小尺寸符号, 48 起圆角底板几乎铺满画布
for (const s of [16, 24, 32]) tmp(`app-${s}.png`, glyphSvg(C1, s))
for (const s of [48, 64, 128, 256]) tmp(`app-${s}.png`, tileSvg(s, 0.78, 'rounded', 0.02))
// 托盘: Windows 多帧 ico 覆盖 100%-200% 缩放, 32px 另存一份作未读角标的底图
for (const s of [16, 20, 24, 32]) tmp(`tray-${s}.png`, glyphSvg(C1, s))
write(path.join(DESK, 'build/tray/tray-32.png'), png(glyphSvg(C1, 32)))
// macOS 菜单栏 template: 纯黑 + alpha, 系统按主题着色
write(path.join(DESK, 'build/tray/menubar.png'), png(glyphSvg('#000', 18)))
write(path.join(DESK, 'build/tray/menubar@2x.png'), png(glyphSvg('#000', 36, 1)))

// 桌面端界面: 左侧导航 32px 用小尺寸符号, 「关于」48px 用应用图标; favicon 16 / 32 符号 + 48 应用图标
write(path.join(DESK, 'frontend/public/glyph.svg'), glyphSvg(C1))
write(path.join(DESK, 'frontend/public/app-icon.png'), png(tileSvg(96, 0.78, 'rounded')))
for (const s of [16, 32]) tmp(`fav-${s}.png`, glyphSvg(C1, s))
tmp('fav-48.png', tileSvg(48, 0.78, 'rounded'))

// 手机看笔记网页
write(path.join(NOTE, 'app/icon.svg'), glyphSvg(C1, 256, 8))
write(path.join(NOTE, 'public/images/icon-192.png'), png(tileSvg(192, 0.78, 'rounded')))
write(path.join(NOTE, 'public/images/icon-512.png'), png(tileSvg(512, 0.78, 'rounded')))
write(path.join(NOTE, 'public/images/apple-touch-icon.png'), png(tileSvg(180, 0.78, 'full')))
write(path.join(NOTE, 'public/images/icon-maskable-512.png'), png(tileSvg(512, 0.6, 'full')))

// Android: 旧版方形 / 圆形图标 (48dp), 自适应图标前景 (108dp, 标志落在中间 66dp 安全圆内), 启动屏
const DENS = { mdpi: 1, hdpi: 1.5, xhdpi: 2, xxhdpi: 3, xxxhdpi: 4 }
for (const [d, k] of Object.entries(DENS)) {
  write(path.join(RES, `mipmap-${d}/ic_launcher.png`), png(tileSvg(48 * k, 0.78, 'rounded', 0.04)))
  write(path.join(RES, `mipmap-${d}/ic_launcher_round.png`), png(tileSvg(48 * k, 0.66, 'circle')))
  write(path.join(RES, `mipmap-${d}/ic_launcher_foreground.png`), png(svg(108 * k, 108 * k, gradDef + placeMark(108 * k, 108 * k, 0.54, 'url(#g)'))))
}
const splash = (w, h) => svg(w, h, gradDef + `<rect width="${w}" height="${h}" fill="${SPLASH_BG}"/>` + placeMark(w, h, (0.36 * Math.min(w, h)) / w, 'url(#g)'))
for (const file of fs.readdirSync(RES).filter((d) => d.startsWith('drawable'))) {
  const p = path.join(RES, file, 'splash.png')
  if (!fs.existsSync(p)) continue
  const buf = fs.readFileSync(p) // 沿用原尺寸 (PNG IHDR 宽高)
  write(p, png(splash(buf.readUInt32BE(16), buf.readUInt32BE(20))))
}
// 通知小图标: 系统只取 alpha, 白色剪影; 24dp 画布四周留 1dp
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
        <path
            android:fillColor="#FFFFFFFF"
            android:pathData="${glyph.d}" />
    </group>
</vector>
`,
)

// iOS (私有仓库): 1024 满幅白底, build-ico.py 去 alpha
if (fs.existsSync(IOS)) write(path.join(IOS, 'AppIcon-512@2x.png'), png(tileSvg(1024, 0.78, 'full')))

// 验收预览用 (不进仓库)
tmp('tile-60.png', tileSvg(60, 0.78, 'rounded'))
tmp('tile-180.png', tileSvg(180, 0.78, 'full'))
tmp('maskable-512.png', tileSvg(512, 0.6, 'full'))
