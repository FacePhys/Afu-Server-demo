import { useState } from 'react'
import { Alert, Button, Flex, Table, type TableColumnsType } from 'antd'
import type { Page } from '../api/types'
import { useApi } from '../api/useApi'

interface Props<T> {
  columns: TableColumnsType<T>
  fetchPage: (cursor: string) => Promise<Page<T>>
  rowKey: string | ((row: T) => string)
  onRowClick?: (row: T) => void
}

// CursorTable 游标分页表格。筛选条件变化时由父组件换 key 重新挂载，从第一页开始。
export default function CursorTable<T extends object>({ columns, fetchPage, rowKey, onRowClick }: Props<T>) {
  const [cursors, setCursors] = useState<string[]>(['']) // 走过的每一页的游标，最后一个是当前页
  const cursor = cursors[cursors.length - 1]
  const { data, loading, error } = useApi(() => fetchPage(cursor), [cursor])

  return (
    <Flex vertical gap={12}>
      {error && <Alert type="error" title={error} showIcon />}
      <Table<T>
        rowKey={rowKey}
        columns={columns}
        dataSource={data?.items}
        loading={loading}
        pagination={false}
        size="middle"
        scroll={{ x: 'max-content' }}
        onRow={onRowClick ? (row) => ({ onClick: () => onRowClick(row), style: { cursor: 'pointer' } }) : undefined}
      />
      <Flex gap={8} align="center" justify="flex-end">
        <span>第 {cursors.length} 页</span>
        <Button disabled={cursors.length === 1 || loading} onClick={() => setCursors((c) => c.slice(0, -1))}>
          上一页
        </Button>
        <Button
          disabled={!data?.next_cursor || loading}
          onClick={() => data?.next_cursor && setCursors((c) => [...c, data.next_cursor])}
        >
          下一页
        </Button>
      </Flex>
    </Flex>
  )
}
