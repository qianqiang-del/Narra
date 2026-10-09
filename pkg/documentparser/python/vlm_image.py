#!/usr/bin/env python3
"""视觉语言模型（VLM）后端：OpenAI 兼容的 POST /chat/completions。

⚠️ 与 rapidocr 不同，这条路径会把图片发送到外部服务，因此只有在设置页启用了
视觉模型配置（且由 Go 侧下发 --vlm-api-url / --vlm-api-model）时才会被导入使用。

协议与 Go 侧设置页的连通性测试（pkg/vlm/client.go）保持一致：base URL 拼
/chat/completions，图片以 data:image/png;base64 放进 image_url.url。改动任一处时
记得同步另一处。

只依赖标准库 urllib（与 api_ocr.py 同一考虑：少一个依赖就少一处版本冲突）；
缩图复用 docling 依赖里已有的 Pillow，缺了它也能照常发原图。
"""

import base64
import io
import json
import mimetypes
import sys
import urllib.error
import urllib.request
from pathlib import Path

# 单张图片的视觉请求超时（秒）；调用方（--vlm-timeout）可覆盖。
DEFAULT_REQUEST_TIMEOUT = 120

# 送图前的长边上限（像素）。视觉 token 按分辨率折算，上传的截图动辄 4K，
# 不缩图就是在为无意义的像素付费；2048 对文字与图表细节都够用。
MAX_IMAGE_EDGE = 2048

# 描述长度上限（字符）。描述是正文的增量信息，太长会把切片预算吃掉。
MAX_DESCRIPTION_CHARS = 1200

# 提示词刻意要求"只描述、不逐字转写"：图上文字的精确提取由同一条链路的本地 OCR
# 负责，各自做擅长的事，也避免同一段文字在正文里出现两遍。但图表的关键数值与
# 结论必须保留 —— 那正是 OCR 拿不到、而检索用户会问的部分。
PROMPT = (
    "请阅读这张图片，用简洁的 Markdown 输出一段描述：\n"
    "- 图表 / 流程图：说明主题、关键数据、步骤与结论；\n"
    "- 截图 / 文档照片：概括其主题与用途；\n"
    "- 一般照片：描述画面内容。\n"
    "不要逐字转写图上的全部文字（另有 OCR 负责），但图表中的关键数值、"
    "标签与结论必须保留。只输出描述本身，不要输出思考过程、解释或代码块标记。"
)


def _mime_type(path: Path) -> str:
    """按扩展名推断 MIME，未知时按 PNG 处理。"""
    guessed, _ = mimetypes.guess_type(str(path))
    return guessed or "image/png"


def _shrink(data: bytes, mime: str) -> tuple[bytes, str]:
    """超过长边上限时等比缩小，返回新的字节与 MIME。

    Pillow 缺失（解析环境没装全）时原样返回 —— 缩图是控成本的优化，不是正确性前提。
    解码失败同样原样返回：真让上游去报"图片无法解析"，错误信息比这里猜的更准。
    """
    try:
        from PIL import Image
    except ImportError:
        return data, mime
    try:
        with Image.open(io.BytesIO(data)) as image:
            if max(image.size) <= MAX_IMAGE_EDGE:
                return data, mime
            image.thumbnail((MAX_IMAGE_EDGE, MAX_IMAGE_EDGE))
            buffer = io.BytesIO()
            image.convert("RGB").save(buffer, format="PNG")
            return buffer.getvalue(), "image/png"
    except Exception:  # noqa: BLE001 - 交给上游报错，这里不吞掉原始数据
        return data, mime


def _content(body: dict) -> str:
    """从 OpenAI 兼容响应里取出文本，兼容 content 为字符串或分段数组两种形态。"""
    try:
        content = body["choices"][0]["message"]["content"]
    except (KeyError, IndexError, TypeError) as exc:
        raise RuntimeError(f"视觉模型响应结构异常: {str(body)[:200]}") from exc
    if isinstance(content, list):
        return "\n".join(
            str(part.get("text", "")) for part in content if isinstance(part, dict)
        )
    return str(content or "")


def _post(
    base_url: str,
    api_key: str,
    model: str,
    image_bytes: bytes,
    mime: str,
    timeout: int,
) -> str:
    """提交一张图片并返回描述。失败时抛 RuntimeError，由上层降级为纯 OCR。"""
    if not base_url:
        raise RuntimeError("未配置视觉模型服务地址")
    if not model:
        raise RuntimeError("未配置视觉模型名称")

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
        "max_tokens": 1024,
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
        with urllib.request.urlopen(request, timeout=timeout) as response:
            body = json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", "ignore")[:200]
        raise RuntimeError(f"视觉模型返回 {exc.code}: {detail}") from exc
    except urllib.error.URLError as exc:
        raise RuntimeError(f"视觉模型请求失败: {exc.reason}") from exc
    return _content(body)


def describe_image(
    path: Path,
    base_url: str,
    api_key: str,
    model: str,
    timeout: int = DEFAULT_REQUEST_TIMEOUT,
) -> str:
    """识别单张图片，返回 Markdown 描述（已按上限裁剪）。"""
    data = Path(path).read_bytes()
    data, mime = _shrink(data, _mime_type(Path(path)))
    description = _post(base_url, api_key, model, data, mime, timeout).strip()
    if len(description) > MAX_DESCRIPTION_CHARS:
        description = description[:MAX_DESCRIPTION_CHARS].rstrip() + "…"
    return description


def describe_image_bytes(
    data: bytes,
    mime: str,
    base_url: str,
    api_key: str,
    model: str,
    timeout: int = DEFAULT_REQUEST_TIMEOUT,
) -> str:
    """与 describe_image 相同，但直接接收字节（PDF 渲染页走这条）。"""
    data, mime = _shrink(data, mime)
    description = _post(base_url, api_key, model, data, mime, timeout).strip()
    if len(description) > MAX_DESCRIPTION_CHARS:
        description = description[:MAX_DESCRIPTION_CHARS].rstrip() + "…"
    return description


__all__ = ["describe_image", "describe_image_bytes", "DEFAULT_REQUEST_TIMEOUT"]
