#!/usr/bin/env python3
"""图片占位符替换模块。

Docling 的 ``export_to_markdown()`` 会在图片位置留下 ``<!-- image -->`` 占位符。
这里把它换成标准的 Markdown 图片引用，alt 文本用 OCR 结果填充 —— 这样即使图片
还没上传到对象存储（或用户用的是纯文本客户端），正文里的文字信息也不会丢。
"""

import sys
from pathlib import Path
from typing import Callable

# Docling 输出的图片占位符，替换时按出现顺序逐个消费
PLACEHOLDER = "<!-- image -->"

# alt 文本上限：Markdown 的 alt 不是正文，太长只会把标题行撑爆
MAX_ALT_CHARS = 200


def _flatten(text: str) -> str:
    """把 OCR 结果压成单行 alt 文本，并去掉会破坏 Markdown 语法的字符。"""
    flat = " ".join((text or "").split())
    for char in "[]()<>!`*_":
        flat = flat.replace(char, " ")
    flat = " ".join(flat.split())
    if len(flat) > MAX_ALT_CHARS:
        flat = flat[:MAX_ALT_CHARS].rstrip() + "…"
    return flat


def build_image_markdown(image_path: Path, recognizer: Callable[[Path], str]) -> str:
    """返回图片的 Markdown 引用。

    OCR 失败时退化为不带 alt 的引用而不是抛异常：图片本身已经导出成功，
    不该因为识别失败就把整篇解析结果弄挂。
    """
    url = Path(image_path).as_posix()
    try:
        alt = _flatten(recognizer(image_path))
    except Exception as exc:  # noqa: BLE001 - OCR 是尽力而为的一步
        print(f"图片 OCR 失败: {exc}", file=sys.stderr)
        alt = ""
    return f"![{alt}]({url})"


__all__ = ["PLACEHOLDER", "build_image_markdown"]
