# 验证截图内容：暗色背景 + 语义色像素 + 非空白
import sys

from PIL import Image

files = {
    "graph-workspace-global.png": None,
    "graph-workspace-focus.png": None,
    "graph-workspace-inspector.png": None,
    "graph-workspace-mobile.png": None,
}

for name in files:
    im = Image.open(f"docs/screenshots/{name}").convert("RGB")
    w, h = im.size
    px = im.load()
    # 统计关键色像素
    targets = {
        "暗色背景#06111e(6,17,30)": (6, 17, 30),
        "画布#071522(7,21,34)": (7, 21, 34),
        "面板#0a1725(10,23,37)": (10, 23, 37),
        "选中青#26d9e8": (38, 217, 232),
        "上游蓝#3f97ff": (63, 151, 255),
        "下游橙#f59e32": (245, 158, 50),
        "白色(248,250,252)": (248, 250, 252),
    }
    counts = {k: 0 for k in targets}
    step = 4  # 抽样
    for y in range(0, h, step):
        for x in range(0, w, step):
            c = px[x, y]
            for label, t in targets.items():
                if abs(c[0] - t[0]) <= 6 and abs(c[1] - t[1]) <= 6 and abs(c[2] - t[2]) <= 6:
                    counts[label] += 1
    print(f"=== {name} ({w}x{h}) ===")
    for label, n in counts.items():
        print(f"  {label}: {n}")
