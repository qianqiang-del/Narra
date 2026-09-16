import { request } from './client'

/** 后端 `dto.MCPServerItem` 的原样形状 */
interface McpServerDTO {
  id: number
  server_id: string
  name: string
  enabled: boolean
  required: boolean
  transport: string
  endpoint: string
  has_api_key: boolean
  startup_timeout: string
  discovery_timeout: string
  call_timeout: string
  sort_order: number
  created_at: string
  updated_at: string
}

export interface McpServer {
  id: number
  serverId: string
  name: string
  enabled: boolean
  required: boolean
  transport: string
  endpoint: string
  hasApiKey: boolean
  startupTimeout: string
  discoveryTimeout: string
  callTimeout: string
  sortOrder: number
  createdAt: string
  updatedAt: string
}

export interface CreateMcpServerInput {
  serverId: string
  name: string
  enabled: boolean
  required: boolean
  transport: string
  endpoint: string
  apiKey: string
  authEnv: string
  startupTimeout: string
  discoveryTimeout: string
  callTimeout: string
  sortOrder: number
}

function toMcpServer(d: McpServerDTO): McpServer {
  return {
    id: d.id,
    serverId: d.server_id,
    name: d.name,
    enabled: d.enabled,
    required: d.required,
    transport: d.transport,
    endpoint: d.endpoint,
    hasApiKey: d.has_api_key,
    startupTimeout: d.startup_timeout,
    discoveryTimeout: d.discovery_timeout,
    callTimeout: d.call_timeout,
    sortOrder: d.sort_order,
    createdAt: d.created_at,
    updatedAt: d.updated_at,
  }
}

/** 拉取 MCP 服务列表。后端已按 sort_order 升序排好。 */
export async function fetchMcpServers(): Promise<McpServer[]> {
  const list = await request<McpServerDTO[]>('/mcp/servers')
  return list.map(toMcpServer)
}

/** 创建 MCP 服务。 */
export async function createMcpServer(input: CreateMcpServerInput): Promise<McpServer> {
  const body = {
    server_id: input.serverId,
    name: input.name,
    enabled: input.enabled,
    required: input.required,
    transport: input.transport,
    endpoint: input.endpoint,
    api_key: input.apiKey,
    auth_env: input.authEnv,
    startup_timeout: input.startupTimeout,
    discovery_timeout: input.discoveryTimeout,
    call_timeout: input.callTimeout,
    sort_order: input.sortOrder,
  }
  const d = await request<McpServerDTO>('/mcp/servers', {
    method: 'POST',
    body: JSON.stringify(body),
  })
  return toMcpServer(d)
}

/** 部分更新 MCP 服务。只传需要修改的字段。 */
export async function updateMcpServer(id: number, input: { enabled?: boolean }): Promise<McpServer> {
  const body: Record<string, unknown> = {}
  if (input.enabled !== undefined) body.enabled = input.enabled
  const d = await request<McpServerDTO>(`/mcp/servers/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(body),
  })
  return toMcpServer(d)
}

/** 删除 MCP 服务。 */
export async function deleteMcpServer(id: number): Promise<void> {
  await request<void>(`/mcp/servers/${id}`, { method: 'DELETE' })
}

/** 测试连接结果 */
export interface McpTestResult {
  success: boolean
  message: string
  tools?: string[]
}

/** 测试与 MCP server 的连接。 */
export async function testMcpServer(id: number): Promise<McpTestResult> {
  return request<McpTestResult>(`/mcp/servers/${id}/test`, { method: 'POST' })
}
