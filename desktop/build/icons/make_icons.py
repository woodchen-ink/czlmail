"""由透明底的 logo.png 生成各平台应用图标。

    python build/icons/make_icons.py   (在 desktop 目录下运行, 需要 pillow 与 numpy)

产物:
  build/appicon.png       macOS: 按 Apple 图标网格, 1024 画布里 824 的圆角方块, 留出投影边距。
                          wails 打包时由它生成 iconfile.icns。
  build/windows/icon.ico  Windows: 圆角方块几乎铺满画布, 任务栏里与其它应用等大。

透明底的横版 logo 直接当图标, 在程序坞里比别的应用小一圈又没有底板, 显得不协调,
所以统一放到白色圆角底板上。
"""

from pathlib import Path

import numpy as np
from PIL import Image, ImageFilter

HERE = Path(__file__).resolve().parent
BUILD = HERE.parent
SS = 4  # 超采样倍数, 让圆角边缘平滑


def squircle_mask(size: int, box: tuple[int, int, int, int], n: float = 5.0) -> Image.Image:
    """超椭圆(连续曲率圆角)蒙版, 近似 macOS 图标的外形。"""
    big = size * SS
    x0, y0, x1, y1 = (v * SS for v in box)
    cx, cy = (x0 + x1) / 2, (y0 + y1) / 2
    rx, ry = (x1 - x0) / 2, (y1 - y0) / 2
    ys, xs = np.mgrid[0:big, 0:big].astype(np.float32) + 0.5
    d = np.abs((xs - cx) / rx) ** n + np.abs((ys - cy) / ry) ** n
    mask = Image.fromarray(((d <= 1.0) * 255).astype(np.uint8), "L")
    return mask.resize((size, size), Image.LANCZOS)


def tile(size: int, box: tuple[int, int, int, int], logo: Image.Image, logo_ratio: float, shadow: bool) -> Image.Image:
    out = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    mask = squircle_mask(size, box)

    if shadow:
        sh = Image.new("RGBA", (size, size), (0, 0, 0, 0))
        offset = round(size * 0.012)
        alpha = mask.point(lambda a: a * 0.32)
        sh.putalpha(alpha)
        sh = sh.transform(sh.size, Image.AFFINE, (1, 0, 0, 0, 1, -offset))
        sh = sh.filter(ImageFilter.GaussianBlur(size * 0.014))
        out = Image.alpha_composite(out, sh)

    # 白色底板, 自上而下极浅的冷灰渐变, 避免纯白在浅色背景上发虚。
    grad = np.linspace(0, 1, size, dtype=np.float32)[:, None]
    top, bottom = np.array([255, 255, 255]), np.array([234, 239, 246])
    rgb = (top + (bottom - top) * grad[..., None]).repeat(size, axis=1).astype(np.uint8)
    plate = Image.fromarray(rgb, "RGB").convert("RGBA")
    plate.putalpha(mask)
    out = Image.alpha_composite(out, plate)

    # logo 按底板宽度缩放后居中, 稍微上移一点做视觉居中(logo 底部更重)。
    bw = box[2] - box[0]
    lw = round(bw * logo_ratio)
    lh = round(logo.height * lw / logo.width)
    lg = logo.resize((lw, lh), Image.LANCZOS)
    cx, cy = (box[0] + box[2]) / 2, (box[1] + box[3]) / 2
    out.alpha_composite(lg, (round(cx - lw / 2), round(cy - lh / 2 - bw * 0.01)))
    return out


def main() -> None:
    src = Image.open(HERE / "logo.png").convert("RGBA")
    # 源图的透明区域里有一层 alpha 只有 1~2 的杂色, 放到白底上会显出一块灰框, 先清掉。
    px = np.array(src)
    px[px[..., 3] < 12] = 0
    src = Image.fromarray(px, "RGBA")
    logo = src.crop(src.getbbox())

    mac = tile(1024, (100, 100, 924, 924), logo, 0.70, shadow=True)
    mac.save(BUILD / "appicon.png")

    win = tile(1024, (24, 24, 1000, 1000), logo, 0.72, shadow=False)
    sizes = [16, 24, 32, 48, 64, 128, 256]
    win.save(BUILD / "windows" / "icon.ico", sizes=[(s, s) for s in sizes])


if __name__ == "__main__":
    main()
