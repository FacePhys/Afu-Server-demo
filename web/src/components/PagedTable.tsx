import { useRef, useState } from 'react'
import { Alert, Button, Flex, Table, Tooltip, type TableColumnsType } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import type { Params } from '../api/client'
import type { Page } from '../api/types'
import { useApi } from '../api/useApi'
import { fmtNum } from '../utils/format'

const PAGE_SIZES = [20, 50, 100]

interface Props<T> {
  columns: TableColumnsType<T>
  /** 拼上筛选条件请求一页；page、page_size、max_id 由表格传入 */
  fetchPage: (params: Params) => Promise<Page<T>>
  rowKey: string | ((row: T) => string)
  onRowClick?: (row: T) => void
}

interface Query {
  page: number
  pageSize: number
  maxId?: number // 快照：第一页由服务端定下，翻页时带上；刷新时清掉
  tick: number // 刷新时加1，页码和条数不变也重新请求
}

// PagedTable 分页表格：总数、页码、跳页、每页条数、刷新。翻页沿用第一页的快照，期间新进来的数据不影响页码；
// 点刷新回到第一页看最新数据。筛选条件变化时由父组件换 key 重新挂载。
export default function PagedTable<T extends object>({ columns, fetchPage, rowKey, onRowClick }: Props<T>) {
  const [query, setQuery] = useState<Query>({ page: 1, pageSize: PAGE_SIZES[0], tick: 0 })
  const { data, loading, error } = useApi(
    () => fetchPage({ page: query.page, page_size: query.pageSize, max_id: query.maxId }),
    [query],
  )
  const top = useRef<HTMLDivElement>(null)

  const refresh = () => setQuery((q) => ({ page: 1, pageSize: q.pageSize, tick: q.tick + 1 }))

  const onChange = (page: number, pageSize: number) => {
    // 换每页条数时回到第一页
    setQuery((q) => ({ ...q, page: pageSize === q.pageSize ? page : 1, pageSize, maxId: data?.max_id ?? q.maxId }))
    // 在表格下方翻页时，滚回表格顶部
    if (top.current && top.current.getBoundingClientRect().top < 0) top.current.scrollIntoView({ behavior: 'smooth' })
  }

  const showTotal = (total: number) => (
    <Flex gap={4} align="center">
      <span>
        {data?.total_capped ? `共 ${fmtNum(total)}+ 条，只能翻看最近 ${fmtNum(total)} 条` : `共 ${fmtNum(total)} 条`}
      </span>
      <Tooltip title="刷新：回到第一页看最新数据">
        <Button type="text" size="small" icon={<ReloadOutlined />} loading={loading} onClick={refresh} />
      </Tooltip>
    </Flex>
  )

  return (
    <div ref={top}>
      <Flex vertical gap={12}>
        {error && <Alert type="error" title={error} showIcon />}
        <Table<T>
          rowKey={rowKey}
          columns={columns}
          dataSource={data?.items}
          loading={loading}
          size="middle"
          scroll={{ x: 'max-content' }}
          onRow={onRowClick ? (row) => ({ onClick: () => onRowClick(row), style: { cursor: 'pointer' } }) : undefined}
          pagination={{
            current: query.page,
            pageSize: query.pageSize,
            total: data?.total ?? 0,
            pageSizeOptions: PAGE_SIZES,
            showSizeChanger: true,
            showQuickJumper: true,
            showTotal,
            onChange,
          }}
        />
      </Flex>
    </div>
  )
}
