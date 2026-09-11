/**
 * 模型服务商静态数据（原项目从服务端配置接口拉取）。
 * 接入 Go 后端后改为 `GET /api/providers`。
 * logo 复用 public/logos/ 下已保留的资源。
 */
export interface ModelItem {
  id: string
  /** 面向用户的短名（未设则展示 id） */
  name?: string
}

export interface ModelProvider {
  id: string
  name: string
  logo: string
  models: ModelItem[]
}

export const MODEL_PROVIDERS: ModelProvider[] = [
  {
    id: 'openai',
    name: 'OpenAI',
    logo: '/logos/openai.svg',
    models: [
      { id: 'gpt-4o-mini' },
      { id: 'gpt-4o' },
      { id: 'gpt-4.1' },
      { id: 'o4-mini' },
    ],
  },
  {
    id: 'deepseek',
    name: 'DeepSeek',
    logo: '/logos/deepseek.svg',
    models: [{ id: 'deepseek-chat' }, { id: 'deepseek-reasoner' }],
  },
  {
    id: 'qwen',
    name: '通义千问',
    logo: '/logos/qwen.svg',
    models: [{ id: 'qwen-max' }, { id: 'qwen-plus' }, { id: 'qwen-turbo' }],
  },
  {
    id: 'glm',
    name: '智谱 GLM',
    logo: '/logos/glm.svg',
    models: [{ id: 'glm-4-plus' }, { id: 'glm-4-air' }, { id: 'glm-4-flash' }],
  },
  {
    id: 'claude',
    name: 'Claude',
    logo: '/logos/claude.svg',
    models: [{ id: 'claude-sonnet-4' }, { id: 'claude-opus-4' }],
  },
  {
    id: 'gemini',
    name: 'Gemini',
    logo: '/logos/gemini.svg',
    models: [{ id: 'gemini-2.5-pro' }, { id: 'gemini-2.5-flash' }],
  },
  {
    id: 'kimi',
    name: 'Kimi',
    logo: '/logos/kimi.png',
    models: [{ id: 'moonshot-v1-128k' }],
  },
  {
    id: 'doubao',
    name: '豆包',
    logo: '/logos/doubao.svg',
    models: [{ id: 'doubao-pro-32k' }],
  },
  {
    id: 'ollama',
    name: 'Ollama',
    logo: '/logos/ollama.svg',
    models: [{ id: 'llama3.3' }, { id: 'qwen2.5' }],
  },
  {
    id: 'openrouter',
    name: 'OpenRouter',
    logo: '/logos/openrouter.svg',
    models: [{ id: 'auto' }],
  },
]
