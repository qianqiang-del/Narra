#!/usr/bin/env python3
"""
文档解析模块

将支持的文档转换为 Markdown 并输出 JSON:
- .docx/.pptx/.xlsx 通过 Docling 转换,文档内图片导出为临时文件并以路径引用
- .pdf 优先提取数字版文字层；只有缺文字层的页才走 OCR —— 整份都没文字层的纯扫描件
  整份 OCR，文字层与扫描页混排的混合型只对缺文字层的扫描页 OCR，再按页号合并
- .jpg/.jpeg/.png/.bmp/.tiff/.tif 使用 RapidOCR 直接识别

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

IMAGE_EXTENSIONS = (".jpg", ".jpeg", ".png", ".bmp", ".tiff", ".tif")


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
                        InputFormat.XLSX: None,
                        InputFormat.PPTX: None,
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


def _extract_pdf_text(path: Path) -> tuple[str, list[dict[str, Any]], list[int]]:
    """提取 PDF 文字层，不碰 Docling 也不碰 OCR。

    除 markdown 与逐页文本外，还返回"没有文字层但含图片"的页号 —— 那才是真正需要
    OCR 的扫描页。没有文字层也没有图片的页（章节分隔之类的空白页）不算在内：对它们
    跑 OCR 只会白白加载一次 OCR 依赖，结果同样是空的。

    图片检测用 ``get_image_info()`` 而不是 ``get_images()``：前者返回"页面上实际显示的
    图片"，连内联图片、嵌在 Form XObject 里的都算 —— 那些正是会让扫描页被漏检、
    进而静默丢内容的形态。``get_images()`` 只看页资源字典里的 Image XObject。

    残留的已知短板：把文字转成曲线的矢量页（无文字层、无图片）仍会被当成空白页跳过。
    这类页面没有便宜的检测办法，且把它们一律送去 OCR 会把装饰性的空白页也识别出噪声。
    """
    import fitz

    pdf = fitz.open(str(path))
    pages: list[dict[str, Any]] = []
    texts: list[str] = []
    scanned_pages: list[int] = []
    try:
        for number, page in enumerate(pdf, start=1):
            text = page.get_text("text").strip()
            pages.append({"number": number, "text": text})
            if text:
                texts.append(f"<!-- page:{number} -->\n{text}")
            elif page.get_image_info():
                scanned_pages.append(number)
    finally:
        pdf.close()
    return "\n\n".join(texts).strip(), pages, scanned_pages


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

    优先用调用方给的 --work-dir（Go 侧会指到一个临时目录，上传对象存储后由它清理）；
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
) -> str:
    """使用 Docling 转换文档；Office 图片导出到 work_dir 下的 images/ 目录。

    导出路径以绝对路径(正斜杠)写入 markdown 引用并加入 picture_paths,
    由 Go 侧上传 MinIO 后回填 URL。单张图片导出失败降级为文本占位,不中断解析。
    """
    converter = _get_docling_converter()
    converted = converter.convert(file_path)
    if converted.status.name != "SUCCESS":
        raise RuntimeError(f"Docling 转换失败: {converted.status}")

    doc = converted.document
    markdown = doc.export_to_markdown()
    if recognizer is None:
        # PDF routing must not OCR extracted images or let their placeholders
        # turn an otherwise empty Docling result into visible content.
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
                replacements.append(build_image_markdown(image_path, recognizer))
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


def _parse_pdf(
    path: Path,
    result: ParseResult,
    ocr_engine: str,
    api_base_url: str,
    api_key: str,
    api_model: str,
    max_ocr_pages: int = 0,
) -> str:
    """逐页决定走文字层还是 OCR。

    按整份判定（"只要任一页有文字就整份用文字层"）会在混合型 PDF 上静默丢内容 ——
    后面的扫描页根本不会出现在结果里，而 Markdown 看上去完全正常。这里改成按页判定：
    文字层覆盖的页保留原文，只有缺文字层的扫描页才送去 OCR，最后按页号合并，
    ``<!-- page:N -->`` 标记与页序都保持不变。
    """
    text_markdown, pages, scanned_pages = _extract_pdf_text(path)
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
        return markdown

    # 有文字层、且没有缺文字层的扫描页：纯文字层路径，完全不加载 OCR 依赖
    # （夹在中间的空白页保持空白，不为它们付一次 OCR 引擎初始化的代价）
    if not scanned_pages:
        result.pages = pages
        print("PDF 解析路径：数字版文字层提取（PyMuPDF）", file=sys.stderr)
        result.metadata.update({"parser": "pymupdf", "fallback": False})
        return text_markdown

    # 混合型：文字层 + 只对扫描页做 OCR，再按页号合并回原始顺序
    if max_ocr_pages > 0 and len(scanned_pages) > max_ocr_pages:
        raise _too_many_ocr_pages(len(scanned_pages), max_ocr_pages)
    _, ocr_pages, parser = _run_pdf_ocr(
        path, ocr_engine, api_base_url, api_key, api_model, scanned_pages
    )
    ocr_by_number = {page["number"]: page for page in ocr_pages}

    merged_pages: list[dict[str, Any]] = []
    blocks: list[str] = []
    for page in pages:
        merged = ocr_by_number.get(page["number"], page)
        merged_pages.append(merged)
        if merged["text"]:
            blocks.append(f"<!-- page:{page['number']} -->\n{merged['text']}")

    result.pages = merged_pages
    print(
        f"PDF 解析路径：混合型（文字层 {len(text_pages)} 页 + "
        f"{parser} OCR {len(scanned_pages)} 页）",
        file=sys.stderr,
    )
    result.metadata.update(
        {
            "parser": parser,
            "fallback": True,
            "fallback_reason": "text_layer_partial",
            "mixed": True,
            "text_layer_pages": len(text_pages),
            "ocr_pages": len(scanned_pages),
        }
    )
    return "\n\n".join(blocks).strip()


def parse_path(
    path: Path,
    ocr_engine: str = "rapidocr",
    api_base_url: str = "",
    api_key: str = "",
    api_model: str = "",
    work_dir: str = "",
    max_ocr_pages: int = 100,
) -> ParseResult:
    """Parse one supported file without writing protocol output."""
    if not path.is_file():
        raise ParserError("PARSER_FILE_NOT_FOUND", "输入文件不存在")
    if ocr_engine not in {"rapidocr", "api"}:
        raise ParserError("PARSER_OCR_ENGINE_INVALID", "不支持的 OCR 引擎")
    if ocr_engine == "api" and not api_base_url:
        raise ParserError("PARSER_OCR_CONFIG_MISSING", "未配置 OCR API 服务地址")

    suffix = path.suffix.lower()
    result = ParseResult(metadata={"file_type": suffix})
    try:
        if suffix in (".docx", ".pptx", ".xlsx"):
            picture_recognizer = _build_picture_recognizer(
                ocr_engine, api_base_url, api_key, api_model
            )
            result.markdown = _convert_with_docling(
                path, result, picture_recognizer, work_dir
            )
            result.metadata["parser"] = "docling"
        elif suffix == ".pdf":
            result.markdown = _parse_pdf(
                path, result, ocr_engine, api_base_url, api_key, api_model, max_ocr_pages
            )
        elif suffix in IMAGE_EXTENSIONS:
            if ocr_engine == "api":
                result.markdown = ocr_image_api(path, api_base_url, api_key, api_model)
                result.metadata["parser"] = "api_ocr"
            else:
                result.markdown = ocr_image(path)
                result.metadata["parser"] = "rapidocr"
        else:
            raise ParserError("PARSER_UNSUPPORTED_TYPE", f"不支持的文档类型: {suffix}")
    except ParserError:
        raise
    except Exception as exc:
        print(f"parser failed: {exc}", file=sys.stderr)
        raise ParserError("PARSER_FAILED", "文档解析失败") from exc
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
    args = parser.parse_args()
    try:
        result = parse_path(
            Path(args.input),
            args.ocr_engine,
            args.ocr_api_url,
            args.ocr_api_key,
            args.ocr_api_model,
            args.work_dir,
            args.max_ocr_pages,
        )
    except ParserError as exc:
        fail(exc.code, exc.message)
    print(json.dumps(asdict(result), ensure_ascii=False))


if __name__ == "__main__":
    main()
