export type MarkdownInlineNode =
  | { type: 'text'; value: string }
  | { type: 'strong'; children: MarkdownInlineNode[] }
  | { type: 'emphasis'; children: MarkdownInlineNode[] }
  | { type: 'code'; value: string }

export type MarkdownBlockNode =
  | { type: 'paragraph'; children: MarkdownInlineNode[] }
  | { type: 'heading'; level: number; children: MarkdownInlineNode[] }
  | { type: 'list'; ordered: boolean; items: MarkdownInlineNode[][] }
  | { type: 'codeBlock'; language: string; value: string }

function normalizeMarkdown(source: string): string {
  return source
    .replace(/\\([*_])/g, '$1')
    .replace(/^(\s*)\\([-+*])(?=\s)/gm, '$1$2')
    .replace(/^(\s*)\\(\d+\.)(?=\s)/gm, '$1$2')
}

function pushText(nodes: MarkdownInlineNode[], value: string) {
  if (!value) return
  const previous = nodes[nodes.length - 1]
  if (previous?.type === 'text') previous.value += value
  else nodes.push({ type: 'text', value })
}

export function parseInline(source: string): MarkdownInlineNode[] {
  const nodes: MarkdownInlineNode[] = []
  let cursor = 0

  while (cursor < source.length) {
    const rest = source.slice(cursor)
    const match = /^(\*\*|__)(.+?)\1|^(\*|_)([^*_\n]+?)\3|^`([^`\n]+)`/.exec(rest)
    if (!match) {
      pushText(nodes, source[cursor])
      cursor += 1
      continue
    }

    if (match[1]) {
      nodes.push({ type: 'strong', children: parseInline(match[2]) })
      cursor += match[0].length
    } else if (match[3]) {
      nodes.push({ type: 'emphasis', children: parseInline(match[4]) })
      cursor += match[0].length
    } else {
      nodes.push({ type: 'code', value: match[5] })
      cursor += match[0].length
    }
  }

  return nodes
}

export function parseMarkdown(source: string): MarkdownBlockNode[] {
  const lines = normalizeMarkdown(source.replace(/\r\n?/g, '\n')).split('\n')
  const nodes: MarkdownBlockNode[] = []
  let paragraph: string[] = []
  let list: { ordered: boolean; items: MarkdownInlineNode[][] } | null = null
  let code: { language: string; lines: string[] } | null = null

  const flushParagraph = () => {
    if (paragraph.length) {
      nodes.push({ type: 'paragraph', children: parseInline(paragraph.join('\n')) })
      paragraph = []
    }
  }
  const flushList = () => {
    if (list) {
      nodes.push({ type: 'list', ordered: list.ordered, items: list.items })
      list = null
    }
  }

  for (const line of lines) {
    const fence = /^```\s*([\w-]*)\s*$/.exec(line)
    if (code) {
      if (fence) {
        nodes.push({ type: 'codeBlock', language: code.language, value: code.lines.join('\n') })
        code = null
      } else code.lines.push(line)
      continue
    }
    if (fence) {
      flushParagraph()
      flushList()
      code = { language: fence[1] ?? '', lines: [] }
      continue
    }

    const heading = /^(#{1,6})\s+(.+)$/.exec(line)
    if (heading) {
      flushParagraph()
      flushList()
      nodes.push({ type: 'heading', level: heading[1].length, children: parseInline(heading[2]) })
      continue
    }

    const item = /^(\s*)([-+*]|\d+\.)\s+(.+)$/.exec(line)
    if (item) {
      flushParagraph()
      const ordered = /^\d/.test(item[2])
      if (!list || list.ordered !== ordered) {
        flushList()
        list = { ordered, items: [] }
      }
      list.items.push(parseInline(item[3]))
      continue
    }

    if (!line.trim()) {
      flushParagraph()
      flushList()
      continue
    }
    flushList()
    paragraph.push(line)
  }

  if (code) nodes.push({ type: 'codeBlock', language: code.language, value: code.lines.join('\n') })
  flushParagraph()
  flushList()
  return nodes
}
