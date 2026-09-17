#!/usr/bin/env python3
"""RapidOCR 本地 OCR 后端。

全程本地 ONNX 推理：不联网、不外发文档内容，与"数据只保存在本地"的部署约定一致。
模块只在真正需要 OCR（扫描版 PDF / 图片）时才被导入，所以重依赖不会拖慢普通解析。
"""

import sys
from pathlib import Path
from typing import Any

# 扫描件渲染精度。太低会掉字，太高会明显变慢 —— 200 DPI 是文字识别的常用折中值
OCR_DPI = 200

_engine: Any | None = None


def _get_engine() -> Any:
    """懒加载 RapidOCR 引擎（进程内单例）。"""
    global _engine
    if _engine is None:
        from rapidocr import RapidOCR

        _engine = RapidOCR()
    return _engine


def _lines_from(raw: Any) -> list[str]:
    """从不同版本的 RapidOCR 返回值里取出文本行。

    rapidocr 3.x 返回带 ``txts`` 的结果对象；2.x / rapidocr_onnxruntime 返回
    ``(result, elapse)`` 二元组，其中 result 是 ``[box, text, score]`` 列表。
    两种都兼容，避免因升级库把 OCR 整条链路弄挂。
    """
    candidates = [raw]
    if isinstance(raw, tuple) and len(raw) == 2:
        candidates.append(raw[0])
    for candidate in candidates:
        texts = getattr(candidate, "txts", None)
        if texts is not None:
            return [str(text) for text in texts]
    results = candidates[-1]
    lines: list[str] = []
    if isinstance(results, list):
        for item in results:
            if isinstance(item, (list, tuple)) and len(item) >= 2:
                lines.append(str(item[1]))
    return lines


def _ocr(data: Any) -> str:
    """跑一次识别，返回按行拼接的纯文本。"""
    lines = _lines_from(_get_engine()(data))
    return "\n".join(line for line in (raw.strip() for raw in lines) if line)


def ocr_image(image: str | Path) -> str:
    """识别单张图片，返回纯文本。"""
    return _ocr(str(image))


def ocr_pdf(
    path: Path, only_pages: list[int] | None = None
) -> tuple[str, list[dict[str, Any]]]:
    """逐页渲染成位图后 OCR。

    输出与"数字版文字层"分支同构的 ``(markdown, pages)``，页面用
    ``<!-- page:N -->`` 标记分隔，方便下游做页码引用。

    ``only_pages`` 指定时只处理这些页（混合型 PDF 里文字层已覆盖的页不必再烧算力），
    其余页不会出现在返回的 pages 里，由调用方按页号合并。

    识别失败的页在 pages 条目上带 ``failed=True``：单页失败不中断整篇，但下游必须能
    把它和"这页本来就没字"区分开，否则整份 OCR 挂掉只会表现为"文档是空的"。
    """
    import fitz  # PyMuPDF，延迟导入：数字版 PDF 完全不需要它

    wanted = set(only_pages) if only_pages is not None else None
    pdf = fitz.open(str(path))
    pages: list[dict[str, Any]] = []
    blocks: list[str] = []
    try:
        for number, page in enumerate(pdf, start=1):
            if wanted is not None and number not in wanted:
                continue
            entry: dict[str, Any] = {"number": number, "text": ""}
            try:
                image_bytes = page.get_pixmap(dpi=OCR_DPI).tobytes("png")
                entry["text"] = _ocr(image_bytes).strip()
            except Exception as exc:  # noqa: BLE001 - 单页失败不应中断整篇
                print(f"第 {number} 页 OCR 失败: {exc}", file=sys.stderr)
                entry["failed"] = True
            pages.append(entry)
            if entry["text"]:
                blocks.append(f"<!-- page:{number} -->\n{entry['text']}")
    finally:
        pdf.close()
    return "\n\n".join(blocks).strip(), pages


__all__ = ["ocr_image", "ocr_pdf", "OCR_DPI"]
