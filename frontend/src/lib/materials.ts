/**
 * 课程材料在前端的选中态。
 *
 * 与旧实现（只存 name/size）的关键差别：这里保留 `File` 本体，直到提交时
 * 由 HomeView 上传到知识库；上传后跟踪收录状态，ready 的才允许带进建课请求。
 *
 * 状态流转：queued → uploading → pending → ready / failed / rejected
 * 失败或未被接收的材料可以退回 queued 重新上传。
 */
export type SelectedMaterialStatus =
  | 'queued' // 已选中，还没上传
  | 'uploading' // 正在上传（整批请求进行中）
  | 'pending' // 已入库，等待解析 / 切分 / 向量化
  | 'ready' // 收录完成，可以参与生成
  | 'failed' // 上传失败、处理失败或等待超时
  | 'rejected' // 服务端未接收（格式不支持、超限、队列满等）

export interface SelectedMaterial {
  /** 前端稳定 key：同名文件可以选多次，不能用文件名当 key */
  key: string
  file: File
  name: string
  size: number
  status: SelectedMaterialStatus
  /** 收录接口返回的文档 ID；上传前与未被接收时为空 */
  documentId: number | null
  /** 失败/被拒的原因，展示用 */
  error: string
}

/** 去重指纹：同名 + 同大小 + 同修改时间视为同一份材料。 */
export function materialFingerprint(file: File): string {
  return `${file.name}:${file.size}:${file.lastModified}`
}
