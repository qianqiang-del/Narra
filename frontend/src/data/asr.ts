/**
 * ASR（语音识别）服务商与语言静态数据。
 *
 * 原项目在 `lib/audio/constants.ts` 里维护，此处只取界面上暴露的那部分：
 * 2 个内置服务商 + 各自支持的语言。用户自定义服务商（自己填 baseUrl 那种）暂不搬。
 *
 * 接入 Go 后端后替换为 `GET /api/audio/asr`；组件只消费 `ASR_PROVIDERS`，
 * 取数方式变了组件不用动。
 *
 * 注意：原版下拉里显示的就是语言代码本身（`name: l`），这里保持一致，
 * 没有额外做中文化映射。
 */
export interface AsrProvider {
  id: string
  name: string
  /** 服务商 logo，位于 public/logos/ */
  logo: string
  /** 该服务商支持的语言代码 */
  languages: string[]
}

const OPENAI_WHISPER: AsrProvider = {
  id: 'openai-whisper',
  name: 'OpenAI Whisper',
  logo: '/logos/openai.svg',
  // Whisper 官方支持 58 种语言，此处取常用部分
  languages: ['auto', 'zh', 'en', 'ja', 'ko', 'es', 'fr', 'de', 'ru', 'ar', 'pt'],
}

const QWEN_ASR: AsrProvider = {
  id: 'qwen-asr',
  name: 'Qwen ASR (阿里云百炼)',
  logo: '/logos/bailian.svg',
  // Qwen ASR 支持 27 种语言 + auto
  languages: ['auto', 'zh', 'yue', 'en', 'ja', 'ko', 'de', 'fr', 'ru', 'es', 'pt'],
}

export const ASR_PROVIDERS: AsrProvider[] = [OPENAI_WHISPER, QWEN_ASR]

/** 兜底服务商：选择项丢失时回退到它 */
export const DEFAULT_ASR_PROVIDER = OPENAI_WHISPER
