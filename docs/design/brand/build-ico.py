"""打包 ico 并给满幅图标去 alpha; 先跑 build-icons.js 生成中间文件。

用法: python docs/design/brand/build-ico.py   (依赖 Pillow)
"""
import os
import tempfile

from PIL import Image

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
TMP = os.path.join(tempfile.gettempdir(), "czlmail-brand")


def pack(dest: str, prefix: str, sizes: list[int]) -> None:
    """每档用各自渲染的位图, 不让 Pillow 从大图缩。"""
    frames = [Image.open(os.path.join(TMP, f"{prefix}-{s}.png")).convert("RGBA") for s in sizes]
    path = os.path.join(ROOT, dest)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    frames[-1].save(path, format="ICO", sizes=[(s, s) for s in sizes], append_images=frames[:-1])
    print("wrote", dest)


pack("desktop/build/windows/icon.ico", "app", [16, 24, 32, 48, 64, 128, 256])
pack("desktop/build/tray/tray.ico", "tray", [16, 20, 24, 32])
pack("desktop/frontend/app/favicon.ico", "fav", [16, 32, 48])

# 满幅直角图标去 alpha (iOS / maskable 由系统裁形; App Store 拒收带 alpha 的图标)
flat = [
    "web-phone-note/public/images/apple-touch-icon.png",
    "web-phone-note/public/images/icon-maskable-512.png",
    "../czlmail-mobile/ios/App/App/Assets.xcassets/AppIcon.appiconset/AppIcon-512@2x.png",
]
for rel in flat:
    p = os.path.join(ROOT, rel)
    if os.path.exists(p):
        Image.open(p).convert("RGB").save(p, optimize=True)
        print("flattened", rel)
