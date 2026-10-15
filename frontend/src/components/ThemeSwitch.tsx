import { Segmented } from 'antd'
import { useThemeMode, type ThemeMode } from '../theme'

// 顶栏常驻的主题三态切换（自动 / 亮色 / 暗色）。
// 窄屏时本控件随其它操作一起收进抽屉（见 Dashboard），但仍可见可点。
export function ThemeSwitch({ block = false }: { block?: boolean }) {
  const { mode, setMode } = useThemeMode()
  return (
    <Segmented
      size="small"
      block={block}
      value={mode}
      onChange={(value) => setMode(value as ThemeMode)}
      options={[
        { label: '自动', value: 'auto' },
        { label: '亮色', value: 'light' },
        { label: '暗色', value: 'dark' },
      ]}
    />
  )
}
