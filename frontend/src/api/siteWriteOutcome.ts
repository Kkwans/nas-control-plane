export type SiteUnknownOperation = 'create' | 'update' | 'icon-compensation'

/** 写入或补偿可能已经执行；只能只读核对，不能当作普通失败直接重放。 */
export class SiteWriteUnknownError extends Error {
  readonly code = 'SITE_WRITE_UNKNOWN'
  constructor(readonly operation: SiteUnknownOperation, readonly siteId?: string) {
    super(operation === 'icon-compensation'
      ? '站点已创建，图标请求失败，补偿删除结果未确认。图标请求仍可能在服务端完成。'
      : operation === 'create'
        ? '创建请求返回成功状态，但响应无效，创建结果未确认。'
        : '保存请求返回成功状态，但响应无效，保存结果未确认。')
    this.name = 'SiteWriteUnknownError'
  }
}
