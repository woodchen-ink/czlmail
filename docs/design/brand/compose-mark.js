// 一次性拼装脚本: CZL 产品标志骨架 (云朵 C 外框 + 速度线 + 底线) + CZL Mail 信封符号 → mark.json。
// mark.json 才是标志的单一来源, 各尺寸图标由 build-icons.js 从它生成; 本脚本只在调整符号几何时重跑。
// 用法: node docs/design/brand/compose-mark.js <czl-skill>/references/product-logo/skeleton.json
//
// 符号: 随外框前倾的平行四边形信封, 下沿并入底线 (左边与底线左端斜切共线), 正面镂空一道 V 形封口。
const fs = require('fs')
const path = require('path')
const { ring, roundPoly, thickV } = require('./geom')

const sk = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'))

const LEAN = 0.395 // 前倾: 每下降 1 向左移 0.395, 取自骨架底线左端斜切
const TOP = 152 // 信封上沿
const BASE = 373 // 底线上沿
const FOOT = 386 // 信封下沿伸进底线内, 与底线合并
const xl = (y) => 217.26 + LEAN * (BASE - y) // 左边: 骨架底线左端斜切的延长线
const xr = (y) => 652 - LEAN * (y - TOP) // 右边: 与左边平行

const body = roundPoly(
  [
    [xl(TOP), TOP],
    [xr(TOP), TOP],
    [xr(FOOT), FOOT],
    [xl(FOOT), FOOT],
  ],
  [22, 16, 0, 0],
)

// V 形封口: 两端距左右边等距, 顶点在信封中线上
const VY = 206
const APEX = 290
const INSET = 40
const flap = thickV([xl(VY) + INSET, VY], [(xl(APEX) + xr(APEX)) / 2, APEX], [xr(VY) - INSET, VY], 24)

const symbol = ring(body, true) + ring(flap, false)
const out = path.join(__dirname, 'mark.json')
fs.writeFileSync(out, JSON.stringify({ d: sk.d + symbol, w: sk.w, h: sk.h }) + '\n')
console.log('wrote', out)
