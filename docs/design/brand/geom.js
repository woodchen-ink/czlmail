// compose-mark.js / compose-glyph.js 共用的几何工具 (一次性脚本的辅助, 产物 mark.json / glyph.json 才是单一来源)。
// 曲线一律采样成折线: 符号只有圆角与圆头, 采样足够密时与弧线无差别, 也便于统一调整环绕方向。
const f = (n) => +n.toFixed(2)

// 有向面积 (y 向下的屏幕坐标里, 正值为顺时针)
const area = (pts) => {
  let a = 0
  for (let i = 0; i < pts.length; i++) {
    const [x1, y1] = pts[i]
    const [x2, y2] = pts[(i + 1) % pts.length]
    a += x1 * y2 - x2 * y1
  }
  return a / 2
}

// 按指定方向输出闭合子路径: solid=true 与骨架 path 同向 (有向面积为负, 与底线重叠处 nonzero 合并),
// false 反向 (nonzero 下为镂空)
const ring = (pts, solid) => {
  const p = (area(pts) < 0) === solid ? pts : [...pts].reverse()
  return 'M' + p.map(([x, y]) => `${f(x)} ${f(y)}`).join('L') + 'Z'
}

const arc = (cx, cy, r, a0, a1, n = 16) =>
  Array.from({ length: n + 1 }, (_, i) => {
    const a = a0 + ((a1 - a0) * i) / n
    return [cx + r * Math.cos(a), cy + r * Math.sin(a)]
  })

// 多边形圆角: 每个顶点按半径 radii[i] 用二次贝塞尔采样代替尖角 (0 = 保留尖角)
const roundPoly = (pts, radii, n = 10) => {
  const out = []
  pts.forEach((p, i) => {
    const r = radii[i]
    if (!r) return out.push(p)
    const prev = pts[(i - 1 + pts.length) % pts.length]
    const next = pts[(i + 1) % pts.length]
    const towards = (q) => {
      const l = Math.hypot(q[0] - p[0], q[1] - p[1])
      return [p[0] + ((q[0] - p[0]) * r) / l, p[1] + ((q[1] - p[1]) * r) / l]
    }
    const a = towards(prev)
    const b = towards(next)
    for (let k = 0; k <= n; k++) {
      const t = k / n
      out.push([
        (1 - t) ** 2 * a[0] + 2 * (1 - t) * t * p[0] + t * t * b[0],
        (1 - t) ** 2 * a[1] + 2 * (1 - t) * t * p[1] + t * t * b[1],
      ])
    }
  })
  return out
}

// 粗折线 A-B-C 的外轮廓: 两端圆头, 拐点外侧圆角、内侧取两条偏移线的交点
const thickV = (A, B, C, w) => {
  const h = w / 2
  const dir = (P, Q) => {
    const l = Math.hypot(Q[0] - P[0], Q[1] - P[1])
    return [(Q[0] - P[0]) / l, (Q[1] - P[1]) / l]
  }
  const d1 = dir(A, B)
  const d2 = dir(B, C)
  const n1 = [-d1[1], d1[0]] // 左法线 (屏幕坐标)
  const n2 = [-d2[1], d2[0]]
  const ang = (v) => Math.atan2(v[1], v[0])
  const shortest = (a0, a1) => (a1 - a0 > Math.PI ? a1 - 2 * Math.PI : a1 - a0 < -Math.PI ? a1 + 2 * Math.PI : a1)
  // 左侧 (V 的下方 / 外侧): 拐点画圆弧
  const left = [
    [A[0] + n1[0] * h, A[1] + n1[1] * h],
    ...arc(B[0], B[1], h, ang(n1), shortest(ang(n1), ang(n2)), 12),
    [C[0] + n2[0] * h, C[1] + n2[1] * h],
  ]
  // 右侧 (V 的上方 / 内侧): 偏移线交点
  const p1 = [A[0] - n1[0] * h, A[1] - n1[1] * h]
  const p2 = [C[0] - n2[0] * h, C[1] - n2[1] * h]
  const cross = (a, b) => a[0] * b[1] - a[1] * b[0]
  const t = cross([p2[0] - p1[0], p2[1] - p1[1]], d2) / cross(d1, d2)
  const inner = [p1[0] + d1[0] * t, p1[1] + d1[1] * t]
  const capC = arc(C[0], C[1], h, ang(n2), ang(n2) - Math.PI, 12)
  const capA = arc(A[0], A[1], h, ang(n1) + Math.PI, ang(n1), 12)
  return [...left.slice(0, -1), ...capC, inner, ...capA.slice(0, -1)]
}

module.exports = { f, area, ring, arc, roundPoly, thickV }
