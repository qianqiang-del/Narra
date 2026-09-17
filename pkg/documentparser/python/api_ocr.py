#!/usr/bin/env python3
"""通用 OCR API 后端。

⚠️ 与 rapidocr 不同，这条路径会把文档内容发送到外部服务，因此只有在用户显式
选择 ``--ocr-engine api`` 时才会被导入使用。默认按 OpenAI 兼容的视觉接口调用:
``POST {base_url}/chat/completions``。

只依赖标准库 urllib，不额外引入 HTTP 客户端 —— 少一个依赖就少一处版本冲突。
"""

import base64
import json
import mimetypes
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

# 与 rapid_ocr 保持一致，保证两条 OCR 路径的输入质量可比
OCR_DPI = 200

# 单张图片的识别超时（秒）
REQUEST_TIMEOUT = 120

PROMPT = (
    "请逐字识别这张图片中的全部文字，保持原始阅读顺序与段落结构，"
    "只输出文字内容本身，不要添加任何解释、标题或 Markdown 代码块。"
)


def _mime_type(path: Path) -> str:
    """按扩展名推断 MIME，未知时按 PNG 处理。"""
    guessed, _ = mimetypes.guess_type(str(path))
    return guessed or "image/png"


def _content(body: dict[str, Any]) -> str:
    """从 OpenAI 兼容响应里取出文本，兼容 content 为字符串或分段数组两种形态。"""
    try:
        content = body["choices"][0]["message"]["content"]
    except (KeyError, IndexError, TypeError) as exc:
        raise RuntimeError(f"OCR API 响应结构异常: {str(body)[:200]}") from exc
    if isinstance(content, list):
        return "\n".join(
            str(part.get("text", "")) for part in content if isinstance(part, dict)
        )
    return str(content or "")


def _post(base_url: str, api_key: str, model: str, image_bytes: bytes, mime: str) -> str:
    """提交一张图片并返回识别文本。失败时抛 RuntimeError，由上层统一转成 PARSER_FAILED。"""
    if not base_url:
        raise RuntimeError("未配置 OCR API 服务地址")
    if not model:
        raise RuntimeError("未配置 OCR API 模型名称")

    encoded = base64.b64encode(image_bytes).decode("ascii")
    payload = {
        "model": model,
        "messages": [
            {
                "role": "user",
                "content": [
                    {"type": "text", "text": PROMPT},
                    {
                        "type": "image_url",
                        "image_url": {"url": f"data:{mime};base64,{encoded}"},
                    },
                ],
            }
        ],
    }

    headers = {"Content-Type": "application/json"}
    if api_key:
        headers["Authorization"] = f"Bearer {api_key}"

    request = urllib.request.Request(
        base_url.rstrip("/") + "/chat/completions",
        data=json.dumps(payload, ensure_ascii=False).encode("utf-8"),
        headers=headers,
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=REQUEST_TIMEOUT) as response:
            body = json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", "ignore")[:200]
        raise RuntimeError(f"OCR API 返回 {exc.code}: {detail}") from exc
    except urllib.error.URLError as exc:
        raise RuntimeError(f"OCR API 请求失败: {exc.reason}") from exc
    return _content(body)


def ocr_image_api(path: Path, base_url: str, api_key: str, model: str) -> str:
    """识别单张图片。"""
    return _post(base_url, api_key, model, Path(path).read_bytes(), _mime_type(Path(path)))


def ocr_pdf_api(
    path: Path, base_url: str, api_key: str, model: str, only_pages: list[int] | None = None
) -> tuple[str, list[dict[str, Any]]]:
    """逐页渲染后调用 OCR API，输出与本地 OCR 分支同构的 ``(markdown, pages)``。

    ``only_pages`` 与 ``rapid_ocr.ocr_pdf`` 语义一致：只处理指定页，供混合型 PDF
    跳过已有文字层的页。识别失败的页带 ``failed=True``。

    单页超时 120 秒，所以这里最怕的失败形态是"网络慢"被当成"这页没字" ——
    ``failed`` 标记就是用来把两者分开的。
    """
    import fitz  # PyMuPDF，延迟导入

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
                entry["text"] = _post(
                    base_url, api_key, model, image_bytes, "image/png"
                ).strip()
            except Exception as exc:  # noqa: BLE001 - 单页失败不应中断整篇
                print(f"第 {number} 页 OCR API 调用失败: {exc}", file=sys.stderr)
                entry["failed"] = True
            pages.append(entry)
            if entry["text"]:
                blocks.append(f"<!-- page:{number} -->\n{entry['text']}")
    finally:
        pdf.close()
    return "\n\n".join(blocks).strip(), pages


__all__ = ["ocr_image_api", "ocr_pdf_api"]
