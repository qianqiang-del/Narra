/**
 * 角色池。
 *
 * 全应用共用一份。「谁需要谁调 `load()`」——load 幂等，多处同时调用只会发一个请求。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { ApiError } from '@/api/client'
import { fetchRoles } from '@/api/roles'
import type { Role } from '@/types/role'

export const useRolesStore = defineStore('roles', () => {
  const roles = ref<Role[]>([])
  const loading = ref(false)
  const error = ref<ApiError | null>(null)
  /** 是否成功拉到过。失败时为 false，所以重试不需要额外的 force 参数 */
  const loaded = ref(false)

  /**
   * 教师：全局唯一，取 `sort_order` 最小的那个。
   *
   * 池子里**允许**有多个 teacher——数据库的 CHECK 只校验 role_type 合法，不限数量，
   * 而「每堂课恰好一个教师」跨表建不出约束，只能靠应用层保证。前端这一端就在这里兜底：
   * 只认第一个，其余的当不存在（既不展示也选不到）。
   *
   * 后端已按 sort_order 升序返回，所以 `find` 命中的就是最小的那个。
   */
  const teacher = computed<Role | null>(() => roles.value.find((r) => r.roleType === 'teacher') ?? null)

  /** 除教师外的可勾选角色，保持后端的 sort_order */
  const selectable = computed(() => roles.value.filter((r) => r.roleType !== 'teacher'))

  /** 在途请求。并发的 load() 共享同一个 Promise，避免重复打接口 */
  let inflight: Promise<void> | null = null

  async function run() {
    loading.value = true
    error.value = null
    try {
      roles.value = await fetchRoles()
      loaded.value = true
    } catch (e) {
      error.value = e instanceof ApiError ? e : new ApiError(-1, String(e))
    } finally {
      loading.value = false
    }
  }

  function load(): Promise<void> {
    if (loaded.value) return Promise.resolve()
    if (!inflight) inflight = run().finally(() => (inflight = null))
    return inflight
  }

  return { roles, loading, error, loaded, teacher, selectable, load }
})
