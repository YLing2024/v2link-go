import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App as AntdApp, ConfigProvider, theme } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import App from './App'
import { useThemeMode } from './theme'
import './styles/style.css'

// 根组件：antd ConfigProvider（中文 locale；theme.algorithm 随解析后的明暗切 default/dark）。
// 铁律：只切 algorithm + 必要的 cssVar，不做任何自定义 token 映射（antd 默认设计语言）。
// cssVar.key 固定 'v2link'，使 CSS 变量块挂在 .v2link 上，剩余自绘组件（弹窗/表格）可共用同一套变量。
function Root() {
  const { resolved } = useThemeMode()
  return (
    <ConfigProvider
      locale={zhCN}
      button={{ autoInsertSpace: false }}
      theme={{
        algorithm: resolved === 'dark' ? theme.darkAlgorithm : theme.defaultAlgorithm,
        cssVar: { key: 'v2link' },
      }}
    >
      <AntdApp>
        <App />
      </AntdApp>
    </ConfigProvider>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Root />
  </StrictMode>,
)
