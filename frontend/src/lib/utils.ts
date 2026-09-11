/**
 * 类名合并工具，等价于原项目的 `cn()`（clsx + tailwind-merge）。
 *
 * 这里不引入额外依赖，做一个轻量实现：过滤假值后拼接。
 * 如果需要真正的 Tailwind 冲突消解（后者覆盖前者的同名工具类），
 * 可换用 tailwind-merge：
 *   npm i clsx tailwind-merge
 *   export const cn = (...i: ClassValue[]) => twMerge(clsx(i))
 */
export type ClassValue =
  | string
  | number
  | null
  | undefined
  | false
  | ClassValue[]
  | Record<string, boolean | null | undefined>

export function cn(...inputs: ClassValue[]): string {
  const out: string[] = []

  const walk = (v: ClassValue) => {
    if (!v) return
    if (typeof v === 'string' || typeof v === 'number') {
      out.push(String(v))
      return
    }
    if (Array.isArray(v)) {
      v.forEach(walk)
      return
    }
    if (typeof v === 'object') {
      for (const [key, enabled] of Object.entries(v)) {
        if (enabled) out.push(key)
      }
    }
  }

  inputs.forEach(walk)
  return out.join(' ')
}
