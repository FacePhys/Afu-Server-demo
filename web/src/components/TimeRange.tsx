import { useState } from 'react'
import { DatePicker, Flex, Segmented } from 'antd'
import { dayRange, presetRange, type Range } from '../utils/time'

type Preset = 'today' | '7d' | '30d' | 'custom'

const PRESET_DAYS: Record<Exclude<Preset, 'custom'>, number> = { today: 1, '7d': 7, '30d': 30 }

interface Props {
  defaultPreset: Exclude<Preset, 'custom'>
  onChange: (r: Range) => void
}

/** 初始区间，和 defaultPreset 对应，供父组件初始化 state */
export function initialRange(preset: Exclude<Preset, 'custom'>): Range {
  return presetRange(PRESET_DAYS[preset])
}

// TimeRange 今天 / 近7天 / 近30天 / 自定义(按天，最长92天由服务端校验)。
export default function TimeRange({ defaultPreset, onChange }: Props) {
  const [preset, setPreset] = useState<Preset>(defaultPreset)

  return (
    <Flex gap={12} align="center" wrap>
      <Segmented<Preset>
        value={preset}
        options={[
          { value: 'today', label: '今天' },
          { value: '7d', label: '近7天' },
          { value: '30d', label: '近30天' },
          { value: 'custom', label: '自定义' },
        ]}
        onChange={(p) => {
          setPreset(p)
          if (p !== 'custom') onChange(presetRange(PRESET_DAYS[p]))
        }}
      />
      {preset === 'custom' && (
        <DatePicker.RangePicker onChange={(v) => v?.[0] && v[1] && onChange(dayRange(v[0], v[1]))} />
      )}
    </Flex>
  )
}
