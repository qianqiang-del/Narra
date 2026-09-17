<#
.SYNOPSIS
    构建"随发布包携带"的 Python 运行时（离线模式）。

.DESCRIPTION
    产物布局（与 pkg/documentparser 的 findPythonIn 约定一致）：
        <OutDir>/python/python.exe                独立解释器（python-build-standalone，可重定位）
        <OutDir>/python/Lib/site-packages/...     已装好的解析依赖

    把 <OutDir> 整个目录打进发布包，默认位置是与 narra-api 可执行文件同级的 python-runtime/。
    使用者机器上因此不需要预装 Python，也不需要联网 —— 这是在线模式（uv 现场准备）之外的
    另一种选择，两者共用同一套目录约定。

.PARAMETER OutDir
    产物目录，默认 dist/python-runtime。

.PARAMETER PythonVersion
    解释器版本，默认 3.12（docling 要求 >= 3.10）。

.PARAMETER Requirements
    依赖清单，默认取仓库内的解析器清单。

.PARAMETER IndexUrl
    PyPI 镜像地址；国内构建建议填 https://pypi.tuna.tsinghua.edu.cn/simple

.EXAMPLE
    powershell -File tools/build_python_runtime.ps1 -IndexUrl https://pypi.tuna.tsinghua.edu.cn/simple

.NOTES
    需要构建机联网，并已安装 uv（https://docs.astral.sh/uv/）。
    ⚠️ 本脚本尚未在 CI 上实际跑通验证，第一次使用请留意每一步输出。
#>
[CmdletBinding()]
param(
    [string]$OutDir = "dist/python-runtime",
    [string]$PythonVersion = "3.12",
    [string]$Requirements = "pkg/documentparser/python/requirements.txt",
    [string]$IndexUrl = ""
)

$ErrorActionPreference = "Stop"

function Step([string]$Message) {
    Write-Host "==> $Message" -ForegroundColor Cyan
}

if (-not (Get-Command uv -ErrorAction SilentlyContinue)) {
    throw "未找到 uv。请先安装：https://docs.astral.sh/uv/"
}
if (-not (Test-Path $Requirements)) {
    throw "依赖清单不存在: $Requirements"
}

$OutDir = [System.IO.Path]::GetFullPath($OutDir)
$PythonDir = Join-Path $OutDir "python"
$Stage = Join-Path $OutDir "_stage"

Step "清理产物目录 $OutDir"
if (Test-Path $OutDir) { Remove-Item -LiteralPath $OutDir -Recurse -Force }
New-Item -ItemType Directory -Force -Path $Stage | Out-Null

Step "下载独立解释器 Python $PythonVersion 到 $Stage"
$env:UV_PYTHON_INSTALL_DIR = $Stage
if ($IndexUrl) { $env:UV_INDEX_URL = $IndexUrl }
uv python install $PythonVersion

$Cpython = Get-ChildItem -Path $Stage -Directory -Filter "cpython-*" | Select-Object -First 1
if (-not $Cpython) { throw "未在 $Stage 找到 cpython-* 目录，uv 的行为可能变了" }

Step "整理成 <OutDir>/python 布局"
Move-Item -LiteralPath $Cpython.FullName -Destination $PythonDir

if ($IsWindows -or $env:OS -eq "Windows_NT") {
    $PythonExe = Join-Path $PythonDir "python.exe"
} else {
    $PythonExe = Join-Path $PythonDir "bin/python3"
}
if (-not (Test-Path $PythonExe)) { throw "未找到解释器: $PythonExe" }

Step "安装解析依赖（torch-free 组合，体积主要来自 onnxruntime 与 opencv）"
# --system：解释器本身不是 venv，必须显式声明要装进它的 site-packages
uv pip install --python $PythonExe --system -r $Requirements

Step "烟雾测试：脚本能否启动"
$Script = "pkg/documentparser/python/parse_document.py"
if (Test-Path $Script) {
    & $PythonExe $Script --help | Out-Null
    Write-Host "    脚本可启动" -ForegroundColor Green
} else {
    Write-Warning "找不到 $Script，跳过烟雾测试"
}

Step "自检关键依赖"
& $PythonExe -c "import docling, fitz; print('docling + pymupdf OK')"

Remove-Item -LiteralPath $Stage -Recurse -Force -ErrorAction SilentlyContinue

$Size = [math]::Round(((Get-ChildItem $PythonDir -Recurse -File | Measure-Object -Property Length -Sum).Sum / 1MB), 0)
Write-Host "完成。产物: $PythonDir （约 $Size MB）" -ForegroundColor Green
Write-Host "打包时把整个 $OutDir 放到 narra-api 可执行文件同级的 python-runtime/ 即可。"
