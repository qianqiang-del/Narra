#!/usr/bin/env python3
"""
文档解析模块

将支持的文档转换为 Markdown 并输出 JSON:
- .docx 通过 Docling 转换,文档内图片导出为临时文件并以路径引用
- .pdf 优先提取数字版文字层；只有缺文字层的页才走 OCR —— 整份都没文字层的纯扫描件
  整份 OCR，文字层与扫描页混排的混合型只对缺文字层的扫描页 OCR，再按页号合并
- 数字版 PDF 可选提取表格（--pdf-table-mode pymupdf），用 PyMuPDF 的 find_tables()
  把表格转成 Markdown；扫描页不做表格结构还原
- .jpg/.jpeg/.png 使用 RapidOCR 直接识别
- 视觉模型（VLM）由设置页启用：启用后每个视觉单元（扫描页 / 内嵌图 / 独立图片）
  在 OCR 之外额外生成一段图片描述；单次调用失败自动降级为纯 OCR，不中断解析

输出格式:JSON 对象,与 Go 侧 ingestion.ParseResult 字段一一对应:
- markdown: 转换后的 Markdown 文本
- picture_paths: 提取的图片临时文件路径列表(绝对路径,正斜杠)
- pages: 页面信息列表(仅 PDF;OCR 失败的页带 failed=true,与"这页本来没字"区分开)
- metadata: 文件元数据(文件类型、解析器;混合型 PDF 另有 mixed 与页数统计)

错误协议:{"error_code": "...", "error_message": "..."}
"""

import argparse
import base64
import json
import os
import re
import sys
import tempfile
import threading
import time
from collections.abc import Callable
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any

# Windows 下管道输出默认使用 GBK 编码,Go 侧按 UTF-8 解析,必须显式统一编码
if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8")
    sys.stderr.reconfigure(encoding="utf-8")

try:
    from .image_alt import build_image_markdown
except ImportError:
    from image_alt import build_image_markdown

IMAGE_EXTENSIONS = (".jpg", ".jpeg", ".png")


@dataclass
class ParseResult:
    """解析结果,与 Go 侧 ingestion.ParseResult 字段一一对应。"""

    markdown: str = ""
    picture_paths: list[str] = field(default_factory=list)
    pages: list[dict[str, Any]] = field(default_factory=list)
    metadata: dict[str, Any] = field(default_factory=dict)


class ParserError(Exception):
    """A safe parser error that preserves the stable error code."""

    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code
        self.message = message


def fail(code: str, message: str) -> None:
    """输出错误信息并退出程序。"""
    print(json.dumps({"error_code": code, "error_message": message}, ensure_ascii=False))
    raise SystemExit(2)


_docling_converter: Any | None = None
_docling_lock = threading.Lock()


def _get_docling_converter() -> Any:
    """获取 Docling 转换器单例(进程内只加载一次)。"""
    global _docling_converter
    if _docling_converter is None:
        with _docling_lock:
            if _docling_converter is None:
                from docling.datamodel.base_models import InputFormat
                from docling.document_converter import DocumentConverter

                _docling_converter = DocumentConverter(
                    format_options={
                        InputFormat.PDF: None,
                        InputFormat.DOCX: None,
                    }
                )
    return _docling_converter


def ocr_image(image: str | Path | Any) -> str:
    """Call the optional RapidOCR backend only when an image is parsed."""
    try:
        from .rapid_ocr import ocr_image as backend
    except ImportError:
        from rapid_ocr import ocr_image as backend
    return backend(image)


def ocr_pdf(
    path: Path, only_pages: list[int] | None = None
) -> tuple[str, list[dict[str, Any]]]:
    """Call the optional RapidOCR backend only when a PDF is parsed."""
    try:
        from .rapid_ocr import ocr_pdf as backend
    except ImportError:
        from rapid_ocr import ocr_pdf as backend
    return backend(path, only_pages)


PDF_TABLE_MODES = ("off", "pymupdf")


def _table_cell(value: Any) -> str:
    """把单元格内容转成 GFM 安全的一格文本。"""
    text = "" if value is None else str(value)
    text = text.replace("\r\n", "\n").replace("\r", "\n").strip()
    # 竖线和换行会破坏 Markdown 表格结构：前者转义，后者用 HTML 换行。
    text = text.replace("|", "\\|")
    return text.replace("\n", "<br>")


def _table_markdown(table: Any) -> str:
    """把 PyMuPDF 的 Table 转成 GFM Markdown；没有可用结构时返回空串。

    不直接调用 ``Table.to_markdown()``：那个实现对单元格里的 ``|`` 不做转义，
    而知识库正文里出现竖线的概率并不低（公式、代码、分隔符）。这里用
    ``extract()`` 自行序列化，换取可控的转义与空表头兜底。
    """
    try:
        extracted = table.extract()
        rows = [list(row) for row in extracted if isinstance(row, (list, tuple))]
    except Exception as exc:  # noqa: BLE001 - 单个表格失败只回退，不影响整页
        print(f"表格内容提取失败: {exc}", file=sys.stderr)
        return ""
    if not rows:
        return ""

    width = max((len(row) for row in rows), default=0)
    if width < 2:
        return ""

    header_names = list(getattr(getattr(table, "header", None), "names", []) or [])
    header_external = bool(getattr(getattr(table, "header", None), "external", False))
    if header_names:
        header = [_table_cell(name) for name in header_names[:width]]
        body = rows if header_external else rows[1:]
    else:
        header = [_table_cell(cell) for cell in rows[0][:width]]
        body = rows[1:]
    header += [""] * (width - len(header))
    if not any(header):
        # 空表头在 GFM 里仍然合法，但检索时缺少列名；给一个稳定占位。
        header = [f"列{index + 1}" for index in range(width)]
    if not body:
        return ""

    lines = [
        "| " + " | ".join(header) + " |",
        "| " + " | ".join("---" for _ in range(width)) + " |",
    ]
    non_empty = sum(1 for cell in header if cell)
    for row in body:
        cells = [_table_cell(cell) for cell in row[:width]]
        cells += [""] * (width - len(cells))
        non_empty += sum(1 for cell in cells if cell)
        lines.append("| " + " | ".join(cells) + " |")
    if non_empty < 2:
        return ""
    return "\n".join(lines)


def _bbox_intersection_ratio(
    block: tuple[float, float, float, float],
    bbox: tuple[float, float, float, float],
) -> float:
    """返回 block 被 bbox 覆盖的面积比例。"""
    block_width = block[2] - block[0]
    block_height = block[3] - block[1]
    if block_width <= 0 or block_height <= 0:
        return 0.0
    width = min(block[2], bbox[2]) - max(block[0], bbox[0])
    height = min(block[3], bbox[3]) - max(block[1], bbox[1])
    if width <= 0 or height <= 0:
        return 0.0
    return (width * height) / (block_width * block_height)


def _page_text_with_tables(page: Any) -> tuple[str, int, bool]:
    """把一页里的表格替换成 Markdown；返回 ``(文本, 表格数, 是否检测失败)``。

    仅用于有文字层的页面。表格按 bbox 与原文本块做替换，而不是整页重排：
    表格外的标题、段落和阅读顺序都保持原样。
    """
    try:
        finder = page.find_tables()
        tables = list(getattr(finder, "tables", finder))
    except Exception as exc:  # noqa: BLE001 - 表格是增量信息，不能拖垮整页
        print(f"表格检测失败: {exc}", file=sys.stderr)
        return "", 0, True

    table_items: list[tuple[tuple[float, float, float, float], str]] = []
    for table in tables:
        markdown = _table_markdown(table)
        if not markdown:
            continue
        bbox = table.bbox
        table_items.append(
            ((float(bbox[0]), float(bbox[1]), float(bbox[2]), float(bbox[3])), markdown)
        )
    if not table_items:
        return "", 0, False

    items: list[dict[str, Any]] = []
    for block in page.get_text("blocks", sort=True):
        if len(block) < 7 or block[6] != 0:
            continue
        text = str(block[4]).strip()
        if not text:
            continue
        items.append(
            {
                "bbox": (float(block[0]), float(block[1]), float(block[2]), float(block[3])),
                "text": text,
                "table": False,
            }
        )

    for table_bbox, markdown in table_items:
        covered: list[int] = []
        for index, item in enumerate(items):
            if item["table"]:
                continue
            block_bbox = item["bbox"]
            center_x = (block_bbox[0] + block_bbox[2]) / 2
            center_y = (block_bbox[1] + block_bbox[3]) / 2
            inside = (
                table_bbox[0] <= center_x <= table_bbox[2]
                and table_bbox[1] <= center_y <= table_bbox[3]
            )
            if inside or _bbox_intersection_ratio(block_bbox, table_bbox) >= 0.5:
                covered.append(index)
        for index in reversed(covered):
            items.pop(index)
        items.append({"bbox": table_bbox, "text": markdown, "table": True})

    items.sort(key=lambda item: item["bbox"][1])
    text = "\n\n".join(item["text"] for item in items if item["text"].strip())
    return text, len(table_items), False


def _extract_pdf_text(
    path: Path, pdf_table_mode: str = "off"
) -> tuple[str, list[dict[str, Any]], list[int], dict[str, Any]]:
    """提取 PDF 文字层，不碰 Docling 也不碰 OCR。

    除 markdown 与逐页文本外，还返回"没有文字层但含图片"的页号 —— 那才是真正需要
    OCR 的扫描页。没有文字层也没有图片的页（章节分隔之类的空白页）不算在内：对它们
    跑 OCR 只会白白加载一次 OCR 依赖，结果同样是空的。

    图片检测用 ``get_image_info()`` 而不是 ``get_images()``：前者返回"页面上实际显示的
    图片"，连内联图片、嵌在 Form XObject 里的都算 —— 那些正是会让扫描页被漏检、
    进而静默丢内容的形态。``get_images()`` 只看页资源字典里的 Image XObject。

    数字版页面在 ``pdf_table_mode=pymupdf`` 时额外调用 PyMuPDF 的 ``find_tables()``，
    把表格替换成 Markdown；失败或没有可用表格时原样返回纯文本。

    残留的已知短板：把文字转成曲线的矢量页（无文字层、无图片）仍会被当成空白页跳过。
    这类页面没有便宜的检测办法，且把它们一律送去 OCR 会把装饰性的空白页也识别出噪声。
    """
    import fitz

    pdf = fitz.open(str(path))
    pages: list[dict[str, Any]] = []
    texts: list[str] = []
    scanned_pages: list[int] = []
    table_stats: dict[str, Any] = {"tables": 0, "table_pages": [], "failed_pages": []}
    try:
        for number, page in enumerate(pdf, start=1):
            text = page.get_text("text").strip()
            if text and pdf_table_mode == "pymupdf":
                table_text, table_count, table_failed = _page_text_with_tables(page)
                if table_failed:
                    table_stats["failed_pages"].append(number)
                if table_count:
                    text = table_text or text
                    table_stats["tables"] += table_count
                    table_stats["table_pages"].append(number)
            pages.append({"number": number, "text": text})
            if text:
                texts.append(f"<!-- page:{number} -->\n{text}")
            elif page.get_image_info():
                scanned_pages.append(number)
    finally:
        pdf.close()
    return "\n\n".join(texts).strip(), pages, scanned_pages, table_stats


def _run_pdf_ocr(
    path: Path,
    ocr_engine: str,
    api_base_url: str,
    api_key: str,
    api_model: str,
    only_pages: list[int] | None = None,
) -> tuple[str, list[dict[str, Any]], str]:
    """按配置选 OCR 后端跑 PDF，返回 ``(markdown, pages, parser 名)``。"""
    if ocr_engine == "api":
        markdown, pages = ocr_pdf_api(path, api_base_url, api_key, api_model, only_pages)
        return markdown, pages, "api_ocr"
    markdown, pages = ocr_pdf(path, only_pages)
    return markdown, pages, "rapidocr"


# PDF 扫描页渲染精度。与 rapid_ocr / api_ocr 的 OCR_DPI 保持一致：两边看的是同一张
# 渲染图，精度不一致只会让"OCR 与视觉描述对不上"这类问题变得难查。
PDF_RENDER_DPI = 200


def _vision_markdown(description: str, text: str) -> str:
    """组合一个视觉单元的输出：图片描述段 + OCR 文字段（空段自动省略）。"""
    blocks: list[str] = []
    if description.strip():
        blocks.append("## 图片内容\n\n" + description.strip())
    if text.strip():
        blocks.append("## 图中文字\n\n" + text.strip())
    return "\n\n".join(blocks)


class VisionRunner:
    """视觉理解调用器：计数、硬闸门、失败降级与现场记录。

    启用条件是"设置页存在启用中的视觉模型配置"（由 Go 侧把地址与模型名随请求下发）。
    任何一次调用失败都只降级为纯 OCR 并留 stderr 日志 —— 视觉描述是增量信息，不该
    拖垮整份文档；只有"需要的图片数超过闸门"才快速失败（与 max_ocr_pages 同理，
    视觉调用按量计费，不设限能把预算烧穿）。
    """

    def __init__(
        self,
        base_url: str,
        api_key: str,
        model: str,
        timeout: int,
        max_calls: int,
        result: ParseResult,
    ) -> None:
        self.base_url = (base_url or "").strip()
        self.api_key = api_key or ""
        self.model = (model or "").strip()
        self.timeout = timeout if timeout and timeout > 0 else 120
        self.max_calls = max_calls
        self.result = result
        self.calls = 0
        self.fallback = False

    @property
    def enabled(self) -> bool:
        return bool(self.base_url and self.model)

    def __bool__(self) -> bool:
        # 未启用时让 `if vision:` 为假，调用点的判断保持一行。
        return self.enabled

    def describe(self, image: Path) -> str:
        return self._call(image, bytes_mode=False)

    def describe_bytes(self, data: bytes, mime: str) -> str:
        return self._call((data, mime), bytes_mode=True)

    def _call(self, image: Any, bytes_mode: bool) -> str:
        if not self.enabled:
            return ""
        if self.max_calls > 0 and self.calls >= self.max_calls:
            raise ParserError(
                "PARSER_TOO_MANY_VLM_CALLS",
                f"这份文档需要视觉理解的图片/页面超过单次上限 {self.max_calls} 张；"
                "请拆分文件，或调大 document_parser.max_vlm_calls",
            )
        self.calls += 1
        try:
            try:
                from .vlm_image import describe_image, describe_image_bytes
            except ImportError:
                from vlm_image import describe_image, describe_image_bytes
            if bytes_mode:
                data, mime = image
                description = describe_image_bytes(
                    data, mime, self.base_url, self.api_key, self.model, self.timeout
                )
            else:
                description = describe_image(
                    image, self.base_url, self.api_key, self.model, self.timeout
                )
        except Exception as exc:  # noqa: BLE001 - 调用失败只降级，不让整篇解析失败
            print(f"视觉模型调用失败，本次降级为纯 OCR: {exc}", file=sys.stderr)
            self.fallback = True
            return ""
        if not description.strip():
            print("视觉模型返回空描述，本次降级为纯 OCR", file=sys.stderr)
            self.fallback = True
            return ""
        return description

    def finalize(self) -> None:
        """把视觉理解的现场写进 metadata；未启用时什么都不写。"""
        if not self.enabled:
            return
        self.result.metadata["vision"] = "ocr+vlm"
        self.result.metadata["vlm_model"] = self.model
        self.result.metadata["vlm_calls"] = self.calls
        if self.fallback:
            self.result.metadata["vision_fallback"] = True


def _descriptions_for_pages(
    path: Path, pages: list[dict[str, Any]], vision: VisionRunner
) -> dict[int, str]:
    """渲染指定页（只包含"OCR 出了字"的页）并逐页取视觉描述。

    空白页不描述：为一个没有内容的页面付一次视觉调用，只是花钱买噪音。
    单页调用失败由 VisionRunner 记降级并返回空串，不影响其它页。
    """
    wanted = {page["number"] for page in pages if str(page.get("text", "")).strip()}
    if not wanted:
        return {}
    descriptions: dict[int, str] = {}
    import fitz  # PyMuPDF，延迟导入：纯文字层路径完全不需要它

    pdf = fitz.open(str(path))
    try:
        for number, page in enumerate(pdf, start=1):
            if number not in wanted:
                continue
            image_bytes = page.get_pixmap(dpi=PDF_RENDER_DPI).tobytes("png")
            descriptions[number] = vision.describe_bytes(image_bytes, "image/png")
    finally:
        pdf.close()
    return descriptions


def ocr_image_api(path: Path, base_url: str, api_key: str, model: str) -> str:
    """Call the optional API OCR backend only when it is selected."""
    try:
        from .api_ocr import ocr_image_api as backend
    except ImportError:
        from api_ocr import ocr_image_api as backend
    return backend(path, base_url, api_key, model)


def ocr_pdf_api(
    path: Path,
    base_url: str,
    api_key: str,
    model: str,
    only_pages: list[int] | None = None,
) -> tuple[str, list[dict[str, Any]]]:
    """Call the optional API OCR backend only when it is selected."""
    try:
        from .api_ocr import ocr_pdf_api as backend
    except ImportError:
        from api_ocr import ocr_pdf_api as backend
    return backend(path, base_url, api_key, model, only_pages)


def _parse_data_uri(data_uri: str) -> tuple[bytes, str]:
    """解析 data URI,返回 (image_data, mime_type)。"""
    header, base64_data = data_uri.split(",", 1)
    mime_type = header.split(":")[1].split(";")[0]
    return base64.b64decode(base64_data), mime_type


def _build_picture_recognizer(
    engine: str,
    api_base_url: str,
    api_key: str,
    api_model: str,
) -> Callable[[Path], str]:
    """为 Office 内嵌图片创建与命令行配置一致的 OCR 调用器。"""
    if engine == "api":
        return lambda path: ocr_image_api(path, api_base_url, api_key, api_model)
    return lambda path: ocr_image(path)


def _resolve_images_dir(work_dir: str) -> Path:
    """确定图片导出目录。

    优先用调用方给的 --work-dir（Go 侧会指到一个临时目录，发布图片后由它清理）；
    没给就每次解析新建一个系统临时目录 —— 绝不写回输入文件所在目录，避免污染上传目录。
    """
    if work_dir:
        target = Path(work_dir)
        target.mkdir(parents=True, exist_ok=True)
        return target
    return Path(tempfile.mkdtemp(prefix="narra-parse-"))


def _convert_with_docling(
    file_path: Path,
    result: ParseResult,
    recognizer: Callable[[Path], str] | None,
    work_dir: str = "",
    vision: VisionRunner | None = None,
) -> str:
    """使用 Docling 转换文档；Office 图片导出到 work_dir 下的 images/ 目录。

    导出路径以绝对路径(正斜杠)写入 markdown 引用并加入 picture_paths,
    由 Go 侧发布到持久存储后回填 URL。单张图片导出失败降级为文本占位,不中断解析。
    """
    converter = _get_docling_converter()
    converted = converter.convert(file_path)
    if converted.status.name != "SUCCESS":
        raise RuntimeError(f"Docling 转换失败: {converted.status}")

    doc = converted.document
    markdown = doc.export_to_markdown()
    if recognizer is None:
        # PDF 路由不能去 OCR 提取出的图片，也不能让它们的占位符
        # 把本已为空的 Docling 结果变成可见内容。
        return re.sub(r"<!--\s*image\s*-->", "", markdown)

    if not (hasattr(doc, "pictures") and doc.pictures):
        return markdown

    images_dir = _resolve_images_dir(work_dir) / "images"
    images_dir.mkdir(parents=True, exist_ok=True)
    replacements: list[str] = []
    for index, pic in enumerate(doc.pictures):
        uri = str(pic.image.uri) if hasattr(pic, "image") and hasattr(pic.image, "uri") else ""
        if uri.startswith("data:"):
            try:
                image_data, mime_type = _parse_data_uri(uri)
                ext = mime_type.split("/")[-1]
                name = f"img_{int(time.time() * 1_000_000)}_{index}.{ext}"
                image_path = images_dir / name
                image_path.write_bytes(image_data)
                posix = image_path.as_posix()
                result.picture_paths.append(posix)
                image_markdown = build_image_markdown(image_path, recognizer)
                if vision:
                    description = vision.describe(image_path)
                    if description:
                        # 描述接在图片引用之后：alt 里那 200 字的 OCR 摘要放不下
                        # 图表的关键数据，而描述正是为检索这些内容准备的。
                        image_markdown = f"{image_markdown}\n\n**图片内容**：{description}"
                replacements.append(image_markdown)
            except Exception as exc:  # noqa: BLE001
                print(f"图片导出失败: {exc}", file=sys.stderr)
                replacements.append("[图片: 导出失败]")
        else:
            replacements.append("")

    for replacement in replacements:
        # 使用 lambda 避免 replacement 中的反斜杠/分组被 re.sub 解释
        markdown = re.sub(r"<!--\s*image\s*-->", lambda _m: replacement, markdown, count=1)
    return markdown


def _too_many_ocr_pages(count: int, limit: int) -> ParserError:
    """需要 OCR 的页数超限时的错误。

    设这道闸门是为了别让一份几百页的扫描件占满整个解析预算：它是逐页推理，
    页数不设限时大概率撞上解析超时被杀，而重试又是从第 1 页重来 —— 永远过不去。
    快速失败反而把动作交给用户：拆文件，或者调大上限（同时要调大 timeout）。
    """
    return ParserError(
        "PARSER_TOO_MANY_OCR_PAGES",
        f"这份 PDF 有 {count} 页需要 OCR，超过单次上限 {limit} 页；"
        "请先拆分文件，或调大 document_parser.max_ocr_pages"
        "（调大时记得同时调大 document_parser.timeout）",
    )


def _merge_table_metadata(
    metadata: dict[str, Any], pdf_table_mode: str, table_stats: dict[str, Any]
) -> None:
    """把表格提取现场写进 metadata；没有表格且模式下不写，保持输出干净。"""
    if pdf_table_mode != "off":
        metadata["table_mode"] = pdf_table_mode
    if table_stats["tables"]:
        metadata["tables"] = table_stats["tables"]
        metadata["table_pages"] = table_stats["table_pages"]
    if table_stats["failed_pages"]:
        metadata["table_failed_pages"] = table_stats["failed_pages"]


def _parse_pdf(
    path: Path,
    result: ParseResult,
    ocr_engine: str,
    api_base_url: str,
    api_key: str,
    api_model: str,
    pdf_table_mode: str = "off",
    max_ocr_pages: int = 0,
    vision: VisionRunner | None = None,
) -> str:
    """逐页决定走文字层还是 OCR。

    按整份判定（"只要任一页有文字就整份用文字层"）会在混合型 PDF 上静默丢内容 ——
    后面的扫描页根本不会出现在结果里，而 Markdown 看上去完全正常。这里改成按页判定：
    文字层覆盖的页保留原文，只有缺文字层的扫描页才送去 OCR，最后按页号合并，
    ``<!-- page:N -->`` 标记与页序都保持不变。

    数字版页面的表格在 ``pdf_table_mode=pymupdf`` 时由 PyMuPDF ``find_tables()``
    转成 Markdown；扫描页仍只走 OCR，不做表格结构还原。
    """
    text_markdown, pages, scanned_pages, table_stats = _extract_pdf_text(
        path, pdf_table_mode
    )
    text_pages = [page for page in pages if page["text"]]

    # 整份都没有文字层：纯扫描件，走整份 OCR。这里刻意不看图片检测结果 ——
    # 扫描件的图片嵌入方式五花八门，一旦漏检就会让整份文档静默变成空内容。
    if not text_pages:
        if max_ocr_pages > 0 and len(pages) > max_ocr_pages:
            raise _too_many_ocr_pages(len(pages), max_ocr_pages)
        markdown, result.pages, parser = _run_pdf_ocr(
            path, ocr_engine, api_base_url, api_key, api_model
        )
        print(
            f"PDF 解析路径：扫描版 OCR（{parser}，原因：text_layer_empty）",
            file=sys.stderr,
        )
        result.metadata.update(
            {
                "parser": parser,
                "fallback": True,
                "fallback_reason": "text_layer_empty",
            }
        )
        if vision:
            # 视觉描述只加在"OCR 出了字"的页上，并按页号重建分块，页序与标记保持原样。
            descriptions = _descriptions_for_pages(path, result.pages, vision)
            blocks: list[str] = []
            for page in result.pages:
                block = _vision_markdown(
                    descriptions.get(page["number"], ""), page.get("text", "")
                )
                if block:
                    blocks.append(f"<!-- page:{page['number']} -->\n{block}")
            markdown = "\n\n".join(blocks).strip()
        return markdown

    # 有文字层、且没有缺文字层的扫描页：纯文字层路径，完全不加载 OCR 依赖
    # （夹在中间的空白页保持空白，不为它们付一次 OCR 引擎初始化的代价）
    if not scanned_pages:
        result.pages = pages
        print("PDF 解析路径：数字版文字层提取（PyMuPDF）", file=sys.stderr)
        metadata: dict[str, Any] = {"parser": "pymupdf", "fallback": False}
        _merge_table_metadata(metadata, pdf_table_mode, table_stats)
        result.metadata.update(metadata)
        return text_markdown

    # 混合型：文字层 + 只对扫描页做 OCR，再按页号合并回原始顺序
    if max_ocr_pages > 0 and len(scanned_pages) > max_ocr_pages:
        raise _too_many_ocr_pages(len(scanned_pages), max_ocr_pages)
    _, ocr_pages, parser = _run_pdf_ocr(
        path, ocr_engine, api_base_url, api_key, api_model, scanned_pages
    )
    ocr_by_number = {page["number"]: page for page in ocr_pages}
    descriptions: dict[int, str] = {}
    if vision:
        descriptions = _descriptions_for_pages(path, ocr_pages, vision)

    merged_pages: list[dict[str, Any]] = []
    blocks: list[str] = []
    for page in pages:
        merged = ocr_by_number.get(page["number"], page)
        merged_pages.append(merged)
        block = _vision_markdown(descriptions.get(page["number"], ""), merged["text"])
        if block:
            blocks.append(f"<!-- page:{page['number']} -->\n{block}")

    result.pages = merged_pages
    print(
        f"PDF 解析路径：混合型（文字层 {len(text_pages)} 页 + "
        f"{parser} OCR {len(scanned_pages)} 页）",
        file=sys.stderr,
    )
    metadata = {
        "parser": parser,
        "fallback": True,
        "fallback_reason": "text_layer_partial",
        "mixed": True,
        "text_layer_pages": len(text_pages),
        "ocr_pages": len(scanned_pages),
    }
    _merge_table_metadata(metadata, pdf_table_mode, table_stats)
    result.metadata.update(metadata)
    return "\n\n".join(blocks).strip()


def parse_path(
    path: Path,
    ocr_engine: str = "rapidocr",
    api_base_url: str = "",
    api_key: str = "",
    api_model: str = "",
    pdf_table_mode: str = "off",
    work_dir: str = "",
    max_ocr_pages: int = 100,
    vlm_api_url: str = "",
    vlm_api_key: str = "",
    vlm_api_model: str = "",
    vlm_timeout: int = 0,
    max_vlm_calls: int = 100,
) -> ParseResult:
    """Parse one supported file without writing protocol output."""
    if not path.is_file():
        raise ParserError("PARSER_FILE_NOT_FOUND", "输入文件不存在")
    if ocr_engine not in {"rapidocr", "api"}:
        raise ParserError("PARSER_OCR_ENGINE_INVALID", "不支持的 OCR 引擎")
    if ocr_engine == "api" and not api_base_url:
        raise ParserError("PARSER_OCR_CONFIG_MISSING", "未配置 OCR API 服务地址")
    if pdf_table_mode not in PDF_TABLE_MODES:
        raise ParserError("PARSER_PDF_TABLE_MODE_INVALID", "不支持的 PDF 表格模式")
    vlm_api_url = (vlm_api_url or "").strip()
    vlm_api_model = (vlm_api_model or "").strip()
    if bool(vlm_api_url) != bool(vlm_api_model):
        raise ParserError("PARSER_VLM_CONFIG_MISSING", "视觉模型的地址与模型名必须同时提供")

    suffix = path.suffix.lower()
    result = ParseResult(metadata={"file_type": suffix})
    vision = VisionRunner(vlm_api_url, vlm_api_key, vlm_api_model, vlm_timeout, max_vlm_calls, result)
    try:
        if suffix == ".docx":
            picture_recognizer = _build_picture_recognizer(
                ocr_engine, api_base_url, api_key, api_model
            )
            result.markdown = _convert_with_docling(
                path, result, picture_recognizer, work_dir, vision
            )
            result.metadata["parser"] = "docling"
        elif suffix == ".pdf":
            result.markdown = _parse_pdf(
                path,
                result,
                ocr_engine,
                api_base_url,
                api_key,
                api_model,
                pdf_table_mode,
                max_ocr_pages,
                vision,
            )
        elif suffix in IMAGE_EXTENSIONS:
            if ocr_engine == "api":
                text = ocr_image_api(path, api_base_url, api_key, api_model)
                result.metadata["parser"] = "api_ocr"
            else:
                text = ocr_image(path)
                result.metadata["parser"] = "rapidocr"
            result.markdown = _vision_markdown(vision.describe(path), text)
        else:
            raise ParserError("PARSER_UNSUPPORTED_TYPE", f"不支持的文档类型: {suffix}")
    except ParserError:
        raise
    except Exception as exc:
        print(f"parser failed: {exc}", file=sys.stderr)
        raise ParserError("PARSER_FAILED", "文档解析失败") from exc
    vision.finalize()
    return result


def main() -> None:
    """解析命令行参数并执行文档解析,输出 JSON 结果。"""
    parser = argparse.ArgumentParser()
    parser.add_argument("--input")
    parser.add_argument(
        "--ocr-engine",
        choices=("rapidocr", "api"),
        default="rapidocr",
        help="图片/PDF 使用的 OCR 引擎: rapidocr(本地,默认) 或 api(通用 OCR API)",
    )
    parser.add_argument("--ocr-api-url", default="", help="通用 OCR API 接口地址")
    parser.add_argument(
        "--ocr-api-key",
        default=os.environ.get("NARRA_OCR_API_KEY", ""),
        help="通用 OCR API 访问凭证；默认读环境变量 NARRA_OCR_API_KEY，避免密钥出现在进程命令行里",
    )
    parser.add_argument("--ocr-api-model", default="", help="通用 OCR API 模型名称")
    parser.add_argument(
        "--pdf-table-mode",
        choices=PDF_TABLE_MODES,
        default="off",
        help="数字版 PDF 表格提取模式: off(关闭) 或 pymupdf(本地表格检测)",
    )
    parser.add_argument(
        "--work-dir",
        default="",
        help="图片导出根目录；留空则新建系统临时目录（默认不写回输入文件所在目录）",
    )
    parser.add_argument(
        "--max-ocr-pages",
        type=int,
        default=100,
        help="单次解析允许 OCR 的页数上限；超过则快速失败（0 表示不限）",
    )
    parser.add_argument(
        "--vlm-api-url",
        default="",
        help="视觉模型服务地址（OpenAI 兼容，不含 /chat/completions）；留空表示只走 OCR",
    )
    parser.add_argument(
        "--vlm-api-key",
        default=os.environ.get("NARRA_VLM_API_KEY", ""),
        help="视觉模型 API Key；默认读环境变量 NARRA_VLM_API_KEY，避免密钥出现在进程命令行里",
    )
    parser.add_argument("--vlm-api-model", default="", help="视觉模型名称，如 Qwen/Qwen3.5-35B-A3B")
    parser.add_argument(
        "--vlm-timeout",
        type=int,
        default=120,
        help="单张图片的视觉请求超时秒数（默认 120）",
    )
    parser.add_argument(
        "--max-vlm-calls",
        type=int,
        default=100,
        help="单次解析允许调用视觉模型的次数上限（扫描页 + 内嵌图 + 独立图片；0 表示不限）",
    )
    args = parser.parse_args()
    try:
        result = parse_path(
            Path(args.input),
            args.ocr_engine,
            args.ocr_api_url,
            args.ocr_api_key,
            args.ocr_api_model,
            args.pdf_table_mode,
            args.work_dir,
            args.max_ocr_pages,
            args.vlm_api_url,
            args.vlm_api_key,
            args.vlm_api_model,
            args.vlm_timeout,
            args.max_vlm_calls,
        )
    except ParserError as exc:
        fail(exc.code, exc.message)
    print(json.dumps(asdict(result), ensure_ascii=False))


if __name__ == "__main__":
    main()
