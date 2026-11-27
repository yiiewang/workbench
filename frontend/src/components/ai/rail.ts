// 左栏 tab 定义：会话 / 索引状态 / 消息目录（AiChat 与 AiRailPanel 共用）
import type { AIIndexStatus } from '../../api/ai'

export type RailTab = 'sessions' | 'index' | 'toc'

export const RAIL_TABS: { id: RailTab; label: string }[] = [
  { id: 'sessions', label: '会话' },
  { id: 'index', label: '索引' },
  { id: 'toc', label: '目录' },
]

/** 索引明细行：路径拆出文件名，目录只显示一次 */
export type IndexRow = AIIndexStatus['items'][number] & { name: string }
export type IndexGroup = { dir: string; rows: IndexRow[]; chunks: number; errors: number }
