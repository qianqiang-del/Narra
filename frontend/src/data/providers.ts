/**
 * 服务商 logo 静态映射。
 *
 * 真实的可选模型来自后端 `GET /llm/models/available`，这里不再有任何模型目录。
 * 剩下的只有「用户给这条配置起的名字 → 用哪张 logo」这一层展示信息：logo 是前端资源，
 * 后端不认识它，用户也不会为了配个图标去改 Base URL。
 *
 * 按关键词做包含匹配，所以命名成「DeepSeek」「我的通义代理」都能命中；命不中就用通用图标兜底。
 * 匹配顺序即优先级，具体的放前面。
 */
export interface ProviderLogo {
  keywords: string[]
  logo: string
}

export const PROVIDER_LOGOS: ProviderLogo[] = [
  { keywords: ['openrouter'], logo: '/logos/openrouter.svg' },
  { keywords: ['openai', 'gpt'], logo: '/logos/openai.svg' },
  { keywords: ['deepseek'], logo: '/logos/deepseek.svg' },
  { keywords: ['qwen', '通义', '千问', 'bailian', '百炼'], logo: '/logos/qwen.svg' },
  { keywords: ['glm', '智谱', 'zhipu'], logo: '/logos/glm.svg' },
  { keywords: ['claude', 'anthropic'], logo: '/logos/claude.svg' },
  { keywords: ['gemini', 'google'], logo: '/logos/gemini.svg' },
  { keywords: ['kimi', 'moonshot'], logo: '/logos/kimi.png' },
  { keywords: ['doubao', '豆包', 'volc'], logo: '/logos/doubao.svg' },
  { keywords: ['ollama'], logo: '/logos/ollama.svg' },
]

/** 按配置名称猜一张 logo；猜不中返回空串，由调用方决定用什么兜底图标。 */
export function findProviderLogo(name: string): string {
  const lower = name.toLowerCase()
  return PROVIDER_LOGOS.find((p) => p.keywords.some((k) => lower.includes(k)))?.logo ?? ''
}
