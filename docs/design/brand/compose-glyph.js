// 一次性拼装脚本: CZL Mail 小尺寸符号 (≤32px 用: 托盘 / 菜单栏、ico 16-32、favicon、通知小图标) → glyph.json。
// glyph.json 才是小尺寸符号的单一来源, build-icons.js 从它生成; 本脚本只在调整符号几何时重跑。
// 用法: node docs/design/brand/compose-glyph.js
//
// 横版信封 (宽高比约 1.45, 与竖版的单证文档拉开剪影), 只留 V 形封口一处镂空, 不要外框、速度线与底线;
// 放正不前倾, 16px 下边缘不出锯齿。镂空线宽与四周留边在 16px 下各约 2px。
const fs = require('fs')
const path = require('path')
const { ring, roundPoly, thickV } = require('./geom')

const W = 300
const H = 206
const body = roundPoly(
  [
    [0, 0],
    [W, 0],
    [W, H],
    [0, H],
  ],
  [40, 40, 40, 40],
)
const flap = thickV([70, 66], [W / 2, 128], [W - 70, 66], 38)

const out = path.join(__dirname, 'glyph.json')
fs.writeFileSync(out, JSON.stringify({ d: ring(body, true) + ring(flap, false), w: W, h: H }) + '\n')
console.log('wrote', out)
