/**
 * MCP 服务配置。
 *
 * 全应用共用一份。「谁需要谁调 `load()`」——load 幂等，多处同时调用只会发一个请求。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'

import { ApiError } from '@/api/client'
import {
  fetchMcpServers,
  createMcpServer,
  updateMcpServer,
  deleteMcpServer,
  testMcpServer,
  type McpServer,
  type McpTestResult,
  type CreateMcpServerInput,
} from '@/api/mcp'

export const useMcpStore = defineStore('mcp', () => {
  const servers = ref<McpServer[]>([])
  const loading = ref(false)
  const error = ref<ApiError | null>(null)
  const loaded = ref(false)

  let inflight: Promise<void> | null = null

  async function run() {
    loading.value = true
    error.value = null
    try {
      servers.value = await fetchMcpServers()
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

  function refresh() {
    loaded.value = false
    inflight = null
    return load()
  }

  async function add(input: CreateMcpServerInput): Promise<McpServer> {
    const server = await createMcpServer(input)
    await refresh()
    return server
  }

  async function edit(id: number, input: { enabled?: boolean }): Promise<McpServer> {
    const server = await updateMcpServer(id, input)
    await refresh()
    return server
  }

  async function remove(id: number): Promise<void> {
    await deleteMcpServer(id)
    await refresh()
  }

  async function test(id: number): Promise<McpTestResult> {
    return testMcpServer(id)
  }

  return { servers, loading, error, loaded, load, refresh, add, edit, remove, test }
})
