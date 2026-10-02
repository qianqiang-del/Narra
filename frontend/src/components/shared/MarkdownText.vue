<script lang="ts">
import { computed, defineComponent, h, type VNode } from 'vue'

import { parseMarkdown, type MarkdownBlockNode, type MarkdownInlineNode } from '@/lib/markdown'

function renderInline(nodes: MarkdownInlineNode[]): VNode[] {
  return nodes.map((node) => {
    if (node.type === 'text') return h('span', node.value)
    if (node.type === 'code') {
      return h('code', { class: 'rounded bg-black/5 px-1 py-0.5 font-mono text-[0.9em] dark:bg-white/10' }, node.value)
    }
    return h(node.type === 'strong' ? 'strong' : 'em', {}, renderInline(node.children))
  })
}

function renderBlock(node: MarkdownBlockNode): VNode {
  if (node.type === 'paragraph') {
    return h('p', { class: 'whitespace-pre-wrap break-words [overflow-wrap:anywhere]' }, renderInline(node.children))
  }
  if (node.type === 'heading') {
    const tag = `h${node.level}` as keyof HTMLElementTagNameMap
    return h(tag, { class: 'font-semibold leading-6 text-foreground' }, renderInline(node.children))
  }
  if (node.type === 'list') {
    const tag = node.ordered ? 'ol' : 'ul'
    return h(tag, { class: node.ordered ? 'list-decimal space-y-1 pl-5' : 'list-disc space-y-1 pl-5' }, node.items.map((item) => h('li', {}, renderInline(item))))
  }
  return h('pre', { class: 'my-2 max-w-full overflow-x-auto rounded-lg bg-black/[.06] p-3 text-[12px] leading-5 dark:bg-white/[.08]' }, [
    h('code', { class: 'font-mono' }, node.value),
  ])
}

export default defineComponent({
  name: 'MarkdownText',
  props: {
    source: { type: String, required: true },
  },
  setup(props) {
    const blocks = computed(() => parseMarkdown(props.source))
    return () => h('div', { class: 'markdown-text space-y-2' }, blocks.value.map(renderBlock))
  },
})
</script>
